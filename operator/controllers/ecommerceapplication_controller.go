/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	b64 "encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	cachev1alpha1 "github.com/multi-tenancy/operator/api/v1alpha1"

	batch "k8s.io/api/batch/v1"
)

// ECommerceApplicationReconciler reconciles a Memcached object
type ECommerceApplicationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=cache.saas.ecommerce.sample.com,resources=ecommerceapplications,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cache.saas.ecommerce.sample.com,resources=ecommerceapplications/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cache.saas.ecommerce.sample.com,resources=ecommerceapplications/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Memcached object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.10.0/pkg/reconcile

// Used to deserialize connection strings to IBM Cloud services
type PostgresBindingJSON struct {
	Cli      Cli      `json:"cli"`
	Postgres Postgres `json:"postgres"`
}

type Cli struct {
	Arguments   []Argument  `json:"argument"`
	Bin         string      `json:"bin"`
	Certificate Certificate `json:"certificate"`
	Composed    []string    `json:"composed"`
	Environment Environment `json:"environment"`
	Type        string      `json:"type"`
}

type Argument struct {
	arr []string
}

type Certificate struct {
	CertificateAuthority string `json:"certificate_authority"`
	CertificateBase64    string `json:"certificate_base64"`
	Name                 string `json:"name"`
}

type Environment struct {
	PgpPassword   string `json:"PGPASSWORD"`
	PgSslRootCert string `json:"PGSSLROOTCERT"`
}

type Postgres struct {
	Authentication Authentication `json:"authentication"`
	Certificate    Certificate    `json:"certificate"`
	Composed       []string       `json:"composed"`
	Database       string         `json:"database"`
	Hosts          []Hosts        `json:"hosts"`
	Path           string         `json:"path"`
	QueryOptions   QueryOptions   `json:"query_options"`
	Scheme         string         `json:"scheme"`
	Type           string         `json:"type"`
}

type Authentication struct {
	Method   string `json:"method"`
	Password string `json:"password"`
	Username string `json:"username"`
}

type Hosts struct {
	Hostname string `json:"hostname"`
	Port     int    `json:"port"`
}

type QueryOptions struct {
	SslMode string `json:"sslmode"`
}

func (r *ECommerceApplicationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	// Fetch the Memcached instance
	memcached := &cachev1alpha1.ECommerceApplication{}
	err := r.Get(ctx, req.NamespacedName, memcached)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			log.Info("Memcached resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get Memcached")
		return ctrl.Result{}, err
	}

	// Check if the Postgres secret created by IBM Cloud Operator already exists
	secret := &corev1.Secret{}
	err = r.Get(ctx, types.NamespacedName{Name: memcached.Spec.PostgresSecretName, Namespace: memcached.Namespace}, secret)
	if err != nil && errors.IsNotFound(err) {
		log.Info("Secret does not exist, wait for a while")

		return ctrl.Result{RequeueAfter: time.Second * 300}, nil
	} else if err == nil {

		//targetSecretName := fmt.Sprintf("%s%s%s", memcached.Spec.PostgresSecretName, "-", memcached.Spec.TenantName)
		// try to unmarshal the contents of the ICO secret
		var data PostgresBindingJSON
		if err := json.Unmarshal(secret.Data["connection"], &data); err != nil {
			fmt.Println("could not unmarshal:", err)
			return ctrl.Result{}, err
		}

		// Create secrets for backend connection to Postgres
		// Create secret postgres.username
		targetSecretName := "postgres.username"
		targetSecret, err := createSecret(targetSecretName, memcached.Namespace, "POSTGRES_USERNAME", data.Postgres.Authentication.Username)
		// Error creating replicating the secret - requeue the request.
		if err != nil {
			return ctrl.Result{}, err
		}

		err = r.Get(context.TODO(), types.NamespacedName{Name: targetSecret.Name, Namespace: targetSecret.Namespace}, secret)
		if err != nil && errors.IsNotFound(err) {
			log.Info(fmt.Sprintf("Target secret %s doesn't exist, creating it", targetSecretName))
			err = r.Create(context.TODO(), targetSecret)
			if err != nil {
				return ctrl.Result{}, err
			}
		} else {
			log.Info(fmt.Sprintf("Target secret %s exists, updating it now", targetSecretName))
			err = r.Update(context.TODO(), targetSecret)
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		// Create secret postgres.password
		targetSecretName = "postgres.password"
		targetSecret, err = createSecret(targetSecretName, memcached.Namespace, "POSTGRES_PASSWORD", data.Postgres.Authentication.Password)
		// Error creating replicating the secret - requeue the request.
		if err != nil {
			return ctrl.Result{}, err
		}

		err = r.Get(context.TODO(), types.NamespacedName{Name: targetSecret.Name, Namespace: targetSecret.Namespace}, secret)
		if err != nil && errors.IsNotFound(err) {
			log.Info(fmt.Sprintf("Target secret %s doesn't exist, creating it", targetSecretName))
			err = r.Create(context.TODO(), targetSecret)
			if err != nil {
				return ctrl.Result{}, err
			}
		} else {
			log.Info(fmt.Sprintf("Target secret %s exists, updating it now", targetSecretName))
			err = r.Update(context.TODO(), targetSecret)
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		// Create secret postgres.certificate-data
		targetSecretName = "postgres.certificate-data"
		decodeArr, _ := b64.StdEncoding.DecodeString(data.Postgres.Certificate.CertificateBase64)
		certDecoded := string(decodeArr[:])
		targetSecret, err = createSecret(targetSecretName, memcached.Namespace, "POSTGRES_CERTIFICATE_DATA", certDecoded)
		// Error creating replicating the secret - requeue the request.
		if err != nil {
			return ctrl.Result{}, err
		}

		err = r.Get(context.TODO(), types.NamespacedName{Name: targetSecret.Name, Namespace: targetSecret.Namespace}, secret)
		if err != nil && errors.IsNotFound(err) {
			log.Info(fmt.Sprintf("Target secret %s doesn't exist, creating it", targetSecretName))
			err = r.Create(context.TODO(), targetSecret)
			if err != nil {
				return ctrl.Result{}, err
			}
		} else {
			log.Info(fmt.Sprintf("Target secret %s exists, updating it now", targetSecretName))
			err = r.Update(context.TODO(), targetSecret)
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		// Create secret postgres.url
		targetSecretName = "postgres.url"
		postgresUrl := fmt.Sprintf("%s%s%s%d%s%s%s", "jdbc:postgresql://", data.Postgres.Hosts[0].Hostname, ":", data.Postgres.Hosts[0].Port, "/", data.Postgres.Database, "?sslmode=verify-full&sslrootcert=/cloud-postgres-cert")
		targetSecret, err = createSecret(targetSecretName, memcached.Namespace, "POSTGRES_URL", postgresUrl)
		// Error creating replicating the secret - requeue the request.
		if err != nil {
			return ctrl.Result{}, err
		}

		err = r.Get(context.TODO(), types.NamespacedName{Name: targetSecret.Name, Namespace: targetSecret.Namespace}, secret)
		if err != nil && errors.IsNotFound(err) {
			log.Info(fmt.Sprintf("Target secret %s doesn't exist, creating it", targetSecretName))
			err = r.Create(context.TODO(), targetSecret)
			if err != nil {
				return ctrl.Result{}, err
			}
		} else {
			log.Info(fmt.Sprintf("Target secret %s exists, updating it now", targetSecretName))
			err = r.Update(context.TODO(), targetSecret)
			if err != nil {
				return ctrl.Result{}, err
			}
		}

	} else if err != nil {
		return ctrl.Result{}, err
	}

	// Create batch Job to populate Postgres
	// How to make sure this only happens once?
	pgJob, err := createPostgresJob(memcached.Namespace)
	// Error creating replicating the secret - requeue the request.
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.Get(context.TODO(), types.NamespacedName{Name: pgJob.Name, Namespace: pgJob.Namespace}, pgJob)
	if err != nil && errors.IsNotFound(err) {
		log.Info(fmt.Sprintf("Job %s doesn't exist, creating it", pgJob.Name))
		err = r.Create(context.TODO(), pgJob)
		if err != nil {
			return ctrl.Result{}, err
		}
	} else {
		log.Info(fmt.Sprintf("Job %s exists, updating it now", pgJob.Name))
		err = r.Update(context.TODO(), pgJob)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	// Check if the deployment already exists, if not create a new one
	found := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: memcached.Name, Namespace: memcached.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		// Define a new deployment
		dep := r.deploymentForMemcached(memcached)
		log.Info("Creating a new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
		err = r.Create(ctx, dep)
		if err != nil {
			log.Error(err, "Failed to create new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return ctrl.Result{}, err
		}
		// Deployment created successfully - return and requeue
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Deployment")
		return ctrl.Result{}, err
	}

	// Ensure the deployment size is the same as the spec
	size := memcached.Spec.Size
	if *found.Spec.Replicas != size {
		found.Spec.Replicas = &size
		err = r.Update(ctx, found)
		if err != nil {
			log.Error(err, "Failed to update Deployment", "Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)
			return ctrl.Result{}, err
		}
		// Ask to requeue after 1 minute in order to give enough time for the
		// pods be created on the cluster side and the operand be able
		// to do the next update step accurately.
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// Update the Memcached status with the pod names
	// List the pods for this memcached's deployment
	//podList := &corev1.PodList{}
	//listOpts := []client.ListOption{
	//	client.InNamespace(memcached.Namespace),
	//	client.MatchingLabels(labelsForMemcached(memcached.Name)),
	//}
	//if err = r.List(ctx, podList, listOpts...); err != nil {
	//	log.Error(err, "Failed to list pods", "Memcached.Namespace", memcached.Namespace, "Memcached.Name", memcached.Name)
	//	return ctrl.Result{}, err
	//}
	//podNames := getPodNames(podList.Items)

	// Update status.Nodes if needed
	//if !reflect.DeepEqual(podNames, memcached.Status.Nodes) {
	//	memcached.Status.Nodes = podNames
	//	err := r.Status().Update(ctx, memcached)
	//		if err != nil {
	//		log.Error(err, "Failed to update Memcached status")
	//		return ctrl.Result{}, err
	//	}
	//}

	return ctrl.Result{}, nil
}

// deploymentForMemcached returns a memcached Deployment object
func (r *ECommerceApplicationReconciler) deploymentForMemcached(m *cachev1alpha1.ECommerceApplication) *appsv1.Deployment {
	ls := labelsForMemcached(m.Name)
	replicas := m.Spec.Size

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Image: "quay.io/nheidloff/service-catalog:latest",
						Name:  "service-catalog",
						Ports: []corev1.ContainerPort{{
							ContainerPort: 8081,
							Name:          "service-catalog",
						}},
					}},
				},
			},
		},
	}
	// Set Memcached instance as the owner and controller
	ctrl.SetControllerReference(m, dep, r.Scheme)
	return dep
}

// labelsForMemcached returns the labels for selecting the resources
// belonging to the given memcached CR name.
func labelsForMemcached(name string) map[string]string {
	return map[string]string{"app": "memcached", "memcached_cr": name}
}

// getPodNames returns the pod names of the array of pods passed in
func getPodNames(pods []corev1.Pod) []string {
	var podNames []string
	for _, pod := range pods {
		podNames = append(podNames, pod.Name)
	}
	return podNames
}

// SetupWithManager sets up the controller with the Manager.
func (r *ECommerceApplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cachev1alpha1.ECommerceApplication{}).
		Owns(&appsv1.Deployment{}).
		Complete(r)
}

// Create Secret definition
func createSecret(name string, namespace string, key string, value string) (*corev1.Secret, error) {
	m := make(map[string]string)
	//m["POSTGRES_USERNAME"] = data.Postgres.Authentication.Username
	m[key] = value

	return &corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Immutable:  new(bool),
		Data:       map[string][]byte{},
		StringData: m,
		Type:       "Opaque",
	}, nil
}

func createPostgresJob(namespace string) (*batch.Job, error) {
	args := []string{"/bin/sh", "-c", "date; echo Hello from the Kubernetes cluster"}

	return &batch.Job{
		TypeMeta: metav1.TypeMeta{Kind: "Job"},
		ObjectMeta: metav1.ObjectMeta{
			Name:                       "pg",
			GenerateName:               "",
			Namespace:                  metav1.NamespaceDefault,
			SelfLink:                   "",
			UID:                        "",
			ResourceVersion:            "",
			Generation:                 0,
			CreationTimestamp:          metav1.Time{},
			DeletionTimestamp:          &metav1.Time{},
			DeletionGracePeriodSeconds: new(int64),
			Labels:                     map[string]string{},
			Annotations:                map[string]string{},
			OwnerReferences:            []metav1.OwnerReference{},
			Finalizers:                 []string{},
			ClusterName:                "",
			ManagedFields:              []metav1.ManagedFieldsEntry{},
		},
		Spec: batch.JobSpec{
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: namespace},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{Image: "bash"},
						{Args: args},
					},
				},
			},
		},
	}, nil
}
