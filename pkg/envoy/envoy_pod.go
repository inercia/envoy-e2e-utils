package envoy

import (
	"context"
	"fmt"
	"path"
	"strconv"

	"github.com/inercia/kubetnl/pkg/graceful"
	"github.com/inercia/kubetnl/pkg/port"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	watchtools "k8s.io/client-go/tools/watch"
	"k8s.io/klog/v2"
)

const (
	configDirectory = "/etc/envoy"
	configFilename  = "envoy.yaml"

	DefaultEnvoyImage = "envoyproxy/envoy:v1.20.6"
)

var envoyPodContainerName = "envoy"

// EnvoyPodConfig is the configuration of an Envoy Pod.
type EnvoyPodConfig struct {
	BootstrapConfig

	// Name is the name for the pod/configmap/serviceaccount/etc
	Name string

	// Namespace is the namespace for the pod/configmap/serviceaccount/etc
	Namespace string

	// Image is the image used for running the Pod
	Image string

	// the port where the Envoy's Pod is listening
	// TODO: we should support multiple listeners
	ListenerPort int

	// Debug must be set to true for enabling debug
	Debug bool

	// RESTConfig is the config used for accessing the Kubernetes cluster.
	RESTConfig *rest.Config
}

// EnvoyPod is the implementation of a Envoy Pod.
type EnvoyPod struct {
	EnvoyPodConfig

	clientSet *kubernetes.Clientset

	serviceAccount       *corev1.ServiceAccount
	serviceAccountClient v1.ServiceAccountInterface
	configMap            *corev1.ConfigMap
	configMapClient      v1.ConfigMapInterface
	service              *corev1.Service
	serviceClient        v1.ServiceInterface
	pod                  *corev1.Pod
	podClient            v1.PodInterface
}

// NewEnvoyPod creates a new Envoy Pod.
// This does not start the new Pod: Start() must be called for that.
func NewEnvoyPod(config EnvoyPodConfig) (*EnvoyPod, error) {
	if config.Image == "" {
		config.Image = DefaultEnvoyImage
	}
	if config.ListenerPort == 0 {
		config.ListenerPort = 80
	}
	if config.RESTConfig == nil {
		return nil, fmt.Errorf("REST config must be provided")
	}

	cs, err := kubernetes.NewForConfig(config.RESTConfig)
	if err != nil {
		return nil, err
	}

	res := &EnvoyPod{
		EnvoyPodConfig:       config,
		clientSet:            cs,
		serviceClient:        cs.CoreV1().Services(config.Namespace),
		podClient:            cs.CoreV1().Pods(config.Namespace),
		serviceAccountClient: cs.CoreV1().ServiceAccounts(config.Namespace),
		configMapClient:      cs.CoreV1().ConfigMaps(config.Namespace),
	}

	return res, nil
}

// Start starts the Envoy Pod.
// This method blocks until the Pod is ready.
func (o *EnvoyPod) Start(ctx context.Context) error {
	var err error

	klog.V(3).Infof("Generating Envoy's bootstrap configuration...")

	cfg := o.BootstrapConfig
	bootstrapConfig, err := cfg.AsString()
	if err != nil {
		return err
	}
	if err := o.createService(ctx, o.ListenerPort); err != nil {
		return err
	}
	if err := o.createConfigMap(ctx, bootstrapConfig); err != nil {
		return err
	}
	if err := o.createServiceAccount(ctx); err != nil {
		return err
	}
	if err := o.createPod(ctx); err != nil {
		return err
	}

	return nil
}

func (o *EnvoyPod) Stop(ctx context.Context) error {
	if err := o.cleanupService(ctx); err != nil {
		klog.Infof("Cleanup failed... ignored: moving on.")
	}
	if err := o.cleanupPod(ctx); err != nil {
		klog.Infof("Cleanup failed... ignored: moving on.")
	}
	if err := o.cleanupServiceAccount(ctx); err != nil {
		klog.Infof("Cleanup failed... ignored: moving on.")
	}
	if err := o.cleanupConfigMap(ctx); err != nil {
		klog.Infof("Cleanup failed... ignored: moving on.")
	}
	return nil
}

/////////////////////////////////////////////////////////////////////////

// CreateService creates the `Service` that will listen at the list of port mappings
// and send that traffic to the `Pod`.
func (o *EnvoyPod) createService(ctx context.Context, listenerPort int) error {
	var err error

	klog.V(3).Infof("Creating Service %q...", o.Name)
	o.service = getService(o.Name, listenerPort)
	o.service, err = o.serviceClient.Create(ctx, o.service, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("error creating Service: %v", err)
	}

	klog.V(3).Infof("Created Service %q.", o.service.GetObjectMeta().GetName())
	return nil
}

func (o *EnvoyPod) cleanupService(ctx context.Context) error {
	deletePolicy := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{PropagationPolicy: &deletePolicy}

	if o.service != nil {
		klog.V(3).Infof("Cleanup: deleting Service %s ...", o.service.Name)
		err := o.serviceClient.Delete(ctx, o.service.Name, deleteOptions)
		if err != nil {
			klog.Info("Cleanup: error deleting Service %q: %v", o.service.Name, err)
			return err
		}
	}

	return nil
}

func (o *EnvoyPod) createPod(ctx context.Context) error {
	var err error

	klog.V(3).Infof("Creating Pod %q...", o.Name)
	o.pod = getPod(o.Name, o.Image, o.ListenerPort, o.AdminPort)
	o.pod, err = o.podClient.Create(ctx, o.pod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("error creating Pod: %v", err)
	}

	klog.V(3).Infof("Created Pod %q...", o.service.GetObjectMeta().GetName())

	klog.V(3).Infof("Waiting for the Pod to be ready before setting up a SSH connection.")
	watchOptions := metav1.ListOptions{}
	watchOptions.FieldSelector = fields.OneTermEqualSelector("metadata.name", o.Name).String()
	watchOptions.ResourceVersion = o.pod.GetResourceVersion()
	podWatch, err := o.podClient.Watch(ctx, watchOptions)
	if err != nil {
		return fmt.Errorf("error watching Pod %s: %v", o.Name, err)
	}

	_, err = watchtools.UntilWithoutRetry(ctx, podWatch, condPodReady)
	if err != nil {
		if err == watchtools.ErrWatchClosed {
			return fmt.Errorf("error waiting for Pod ready: podWatch has been closed before pod ready event received")
		}

		// err will be wait.ErrWatchClosed is the context passed to
		// watchtools.UntilWithoutRetry is done. However, if the interrupt
		// context was canceled, return an graceful.Interrupted.
		if ctx.Err() != nil {
			return graceful.Interrupted
		}
		if err == wait.ErrWaitTimeout {
			return fmt.Errorf("error waiting for Pod ready: timed out after %d seconds", 300)
		}
		return fmt.Errorf("error waiting for Pod ready: received unknown error \"%f\"", err)
	}

	klog.V(2).Infof("Pod ready...")

	return nil
}

func (o *EnvoyPod) cleanupPod(ctx context.Context) error {
	deletePolicy := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{PropagationPolicy: &deletePolicy}

	if o.pod != nil {
		klog.V(3).Infof("Cleanup: deleting Pod %s ...", o.pod.Name)
		if err := o.podClient.Delete(ctx, o.pod.Name, deleteOptions); err != nil {
			klog.Infof("Cleanup: error deleting Pod %q: %v", o.pod.Name, err)
			return err
		}
	}

	return nil
}

func (o *EnvoyPod) createServiceAccount(ctx context.Context) error {
	var err error

	klog.V(2).Infof("Creating ServiceAccount %q...", o.Name)
	o.serviceAccount = getServiceAccount(o.Name)
	o.serviceAccount, err = o.serviceAccountClient.Create(ctx, o.serviceAccount, metav1.CreateOptions{})
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("error creating ServiceAccount %q: %v", o.serviceAccount.Name, err)
		}
	}

	return nil
}

func (o *EnvoyPod) cleanupServiceAccount(ctx context.Context) error {
	deletePolicy := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{PropagationPolicy: &deletePolicy}

	if o.serviceAccount != nil {
		klog.V(3).Infof("Cleanup: deleting service account %s ...", o.serviceAccount.Name)
		if err := o.serviceAccountClient.Delete(ctx, o.serviceAccount.Name, deleteOptions); err != nil {
			klog.Infof("Cleanup: error deleting ServiceAccount %q: %v", o.serviceAccount.Name, err)
			return err
		}
	}

	return nil
}

func (o *EnvoyPod) createConfigMap(ctx context.Context, contents string) error {
	var err error

	klog.V(3).Infof("Creating ConfigMap %q...", o.Name)
	o.configMap = getConfigMap(o.Name, contents)
	o.configMap, err = o.configMapClient.Create(ctx, o.configMap, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("error creating ConfigMap: %v", err)
	}

	klog.V(3).Infof("Created ConfigMap %q.", o.configMap.GetObjectMeta().GetName())
	return nil
}

func (o *EnvoyPod) cleanupConfigMap(ctx context.Context) error {
	deletePolicy := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{PropagationPolicy: &deletePolicy}

	klog.V(2).Infof("Cleanup: deleting config map %s ...", o.configMap.Name)
	if err := o.configMapClient.Delete(ctx, o.configMap.Name, deleteOptions); err != nil {
		klog.Infof("Cleanup: error deleting config map %q: %v.", o.configMap.Name, err)
		return err
	}

	return nil
}

/////////////////////////////////////////////////////////////////////////

func getService(name string, listenerPort int) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"io.github.envoy-e2e-utils": name,
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"io.github.envoy-e2e-utils": name,
			},
			Ports: []corev1.ServicePort{
				{
					Name:     "http",
					Port:     int32(listenerPort),
					Protocol: corev1.ProtocolTCP,
				},
			},
		},
	}
}

func getConfigMap(name string, contents string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"io.github.envoy-e2e-utils": name,
			},
		},
		Data: map[string]string{
			configFilename: contents,
		},
	}
}

func getServiceAccount(name string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"io.github.envoy-e2e-utils": name,
			},
		},
	}
}

func getPod(name, image string, listenerPort int, readinessPort int) *corev1.Pod {
	ports := []corev1.ContainerPort{
		{
			Name:          "listener",
			ContainerPort: int32(listenerPort),
		},
	}

	// add a couple of things if we have a readiness port
	// IMPORTANT: it is highly recommended to use a port for readiness,
	// as any tunnel to this Pod could fail if the pod is considered ready but it is not.
	var readinessProbe *corev1.Probe
	if readinessPort != 0 {
		readinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt(readinessPort),
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       5,
			FailureThreshold:    3,
		}

		ports = append(ports, corev1.ContainerPort{
			Name:          "readyness",
			ContainerPort: int32(readinessPort),
		})
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"io.github.envoy-e2e-utils": name,
			},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: string(name),
			Containers: []corev1.Container{{
				Name:            envoyPodContainerName,
				Image:           image,
				ImagePullPolicy: corev1.PullPolicy(corev1.PullIfNotPresent),
				Command: []string{
					"envoy",
					"-c",
					path.Join(configDirectory, configFilename),
				},
				Ports: ports,
				Env: []corev1.EnvVar{
					{Name: "PORT", Value: strconv.Itoa(listenerPort)},
				},
				VolumeMounts: []corev1.VolumeMount{{
					Name:      "config",
					MountPath: configDirectory,
				}},
				ReadinessProbe: readinessProbe,
			}},
			Volumes: []corev1.Volume{{
				Name: "config",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: name,
						},
						Items: []corev1.KeyToPath{
							{
								Key:  configFilename,
								Path: configFilename,
							},
						},
					},
				},
			}},
		},
	}
}

func condPodReady(event watch.Event) (bool, error) {
	pod := event.Object.(*corev1.Pod)
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			klog.V(3).Infof("Envoy pod check: it is ready !!")
			return true, nil
		}
	}

	klog.V(3).Infof("Envoy pod check: it is NOT ready yet.")
	return false, nil
}

func protocolToCoreV1(p port.Protocol) corev1.Protocol {
	if p == port.ProtocolSCTP {
		return corev1.ProtocolSCTP
	}
	if p == port.ProtocolUDP {
		return corev1.ProtocolUDP
	}
	return corev1.ProtocolTCP
}
