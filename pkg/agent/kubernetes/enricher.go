package kubernetes

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type Enricher struct {
	client     *kubernetes.Clientset
	config     *Config
	mu         sync.RWMutex
	podCache   map[string]*PodInfo
	nodeCache  map[string]*NodeInfo
	serviceCache map[string]*ServiceInfo
	stopCh     chan struct{}
	logger     *zap.SugaredLogger
}

type Config struct {
	Enabled      bool
	ResyncPeriod time.Duration
	Namespace    string
	KubeConfig   string
}

type PodInfo struct {
	Name            string
	Namespace       string
	UID             string
	Labels         map[string]string
	Annotations     map[string]string
	ServiceAccount  string
	NodeName        string
	HostIP          string
	PodIP           string
	StartTime       *time.Time
	Containers      []ContainerInfo
	OwnerReferences []OwnerRef
}

type ContainerInfo struct {
	Name         string
	Image        string
	Ports        []ContainerPort
	Env          []EnvVar
	VolumeMounts []VolumeMount
}

type ContainerPort struct {
	Name          string
	ContainerPort int32
	Protocol      string
}

type EnvVar struct {
	Name  string
	Value string
}

type VolumeMount struct {
	Name      string
	MountPath string
}

type OwnerRef struct {
	Kind string
	Name string
	UID  string
}

type NodeInfo struct {
	Name             string
	UID              string
	Labels           map[string]string
	Annotations      map[string]string
	InternalIP       string
	ExternalIP       string
	OperatingSystem  string
	Architecture     string
	KubeletVersion   string
	KernelVersion    string
}

type ServiceInfo struct {
	Name        string
	Namespace   string
	UID         string
	Labels      map[string]string
	Annotations map[string]string
	Selector    map[string]string
	ClusterIP   string
	ExternalIP  []corev1.LoadBalancerIngress
	Ports       []ServicePort
}

type ServicePort struct {
	Name       string
	Port       int32
	Protocol   string
	TargetPort int32
}

func NewEnricher(config *Config) (*Enricher, error) {
	logger, _ := zap.NewProduction()

	var client *kubernetes.Clientset
	var err error

	if config.KubeConfig != "" {
		client, err = loadClientFromConfig(config.KubeConfig)
	} else {
		client, err = loadInClusterClient()
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	enricher := &Enricher{
		client:        client,
		config:        config,
		podCache:     make(map[string]*PodInfo),
		nodeCache:    make(map[string]*NodeInfo),
		serviceCache: make(map[string]*ServiceInfo),
		stopCh:       make(chan struct{}),
		logger:       logger.Sugar(),
	}

	if config.Enabled {
		if err := enricher.start(); err != nil {
			return nil, err
		}
	}

	return enricher, nil
}

func loadInClusterClient() (*kubernetes.Clientset, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(config)
}

func loadClientFromConfig(kubeConfig string) (*kubernetes.Clientset, error) {
	config, err := loadKubeConfig(kubeConfig)
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(config)
}

func loadKubeConfig(kubeConfig string) (*rest.Config, error) {
	return nil, fmt.Errorf("not implemented")
}

func (e *Enricher) start() error {
	e.logger.Info("Starting Kubernetes enricher")

	if err := e.syncPods(); err != nil {
		e.logger.Warnf("Initial pod sync failed: %v", err)
	}

	if err := e.syncNodes(); err != nil {
		e.logger.Warnf("Initial node sync failed: %v", err)
	}

	if err := e.syncServices(); err != nil {
		e.logger.Warnf("Initial service sync failed: %v", err)
	}

	go e.runWorker()

	return nil
}

func (e *Enricher) runWorker() {
	ticker := time.NewTicker(e.config.ResyncPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.syncPods()
			e.syncNodes()
			e.syncServices()
		}
	}
}

func (e *Enricher) Stop() {
	close(e.stopCh)
}

func (e *Enricher) syncPods() error {
	ctx := context.Background()

	var pods *corev1.PodList
	var err error

	if e.config.Namespace != "" {
		pods, err = e.client.CoreV1().Pods(e.config.Namespace).List(ctx, metav1.ListOptions{})
	} else {
		pods, err = e.client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	}

	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	e.podCache = make(map[string]*PodInfo)

	for _, pod := range pods.Items {
		podInfo := &PodInfo{
			Name:            pod.Name,
			Namespace:       pod.Namespace,
			UID:             string(pod.UID),
			Labels:          pod.Labels,
			Annotations:     pod.Annotations,
			ServiceAccount:  pod.Spec.ServiceAccountName,
			NodeName:        pod.Spec.NodeName,
			HostIP:          pod.Status.HostIP,
			PodIP:           pod.Status.PodIP,
			Containers:      make([]ContainerInfo, len(pod.Spec.Containers)),
			OwnerReferences: make([]OwnerRef, len(pod.OwnerReferences)),
		}

		if pod.Status.StartTime != nil {
			t := pod.Status.StartTime.Time
			podInfo.StartTime = &t
		}

		for i, container := range pod.Spec.Containers {
			podInfo.Containers[i] = ContainerInfo{
				Name:        container.Name,
				Image:       container.Image,
				Ports:       make([]ContainerPort, len(container.Ports)),
				Env:         make([]EnvVar, len(container.Env)),
				VolumeMounts: make([]VolumeMount, len(container.VolumeMounts)),
			}

			for j, port := range container.Ports {
				podInfo.Containers[i].Ports[j] = ContainerPort{
					Name:          port.Name,
					ContainerPort: port.ContainerPort,
					Protocol:      string(port.Protocol),
				}
			}

			for j, env := range container.Env {
				podInfo.Containers[i].Env[j] = EnvVar{
					Name:  env.Name,
					Value: env.Value,
				}
			}

			for j, vm := range container.VolumeMounts {
				podInfo.Containers[i].VolumeMounts[j] = VolumeMount{
					Name:      vm.Name,
					MountPath: vm.MountPath,
				}
			}
		}

		for i, owner := range pod.OwnerReferences {
			podInfo.OwnerReferences[i] = OwnerRef{
				Kind: owner.Kind,
				Name: owner.Name,
				UID:  string(owner.UID),
			}
		}

		key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		e.podCache[key] = podInfo

		if pod.Status.PodIP != "" {
			e.podCache[pod.Status.PodIP] = podInfo
		}
	}

	e.logger.Infof("Synced %d pods", len(pods.Items))
	return nil
}

func (e *Enricher) syncNodes() error {
	ctx := context.Background()

	nodes, err := e.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	e.nodeCache = make(map[string]*NodeInfo)

	for _, node := range nodes.Items {
		nodeInfo := &NodeInfo{
			Name:            node.Name,
			UID:             string(node.UID),
			Labels:          node.Labels,
			Annotations:     node.Annotations,
			OperatingSystem: node.Status.NodeInfo.OperatingSystem,
			Architecture:    node.Status.NodeInfo.Architecture,
			KubeletVersion:  node.Status.NodeInfo.KubeletVersion,
			KernelVersion:   node.Status.NodeInfo.KernelVersion,
		}

		for _, addr := range node.Status.Addresses {
			switch addr.Type {
			case corev1.NodeInternalIP:
				nodeInfo.InternalIP = addr.Address
			case corev1.NodeExternalIP:
				nodeInfo.ExternalIP = addr.Address
			}
		}

		e.nodeCache[node.Name] = nodeInfo
	}

	e.logger.Infof("Synced %d nodes", len(nodes.Items))
	return nil
}

func (e *Enricher) syncServices() error {
	ctx := context.Background()

	var services *corev1.ServiceList
	var err error

	if e.config.Namespace != "" {
		services, err = e.client.CoreV1().Services(e.config.Namespace).List(ctx, metav1.ListOptions{})
	} else {
		services, err = e.client.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	}

	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	e.serviceCache = make(map[string]*ServiceInfo)

	for _, svc := range services.Items {
		serviceInfo := &ServiceInfo{
			Name:        svc.Name,
			Namespace:   svc.Namespace,
			UID:         string(svc.UID),
			Labels:      svc.Labels,
			Annotations: svc.Annotations,
			Selector:    svc.Spec.Selector,
			ClusterIP:   svc.Spec.ClusterIP,
			ExternalIP:  svc.Status.LoadBalancer.Ingress,
			Ports:       make([]ServicePort, len(svc.Spec.Ports)),
		}

		for i, port := range svc.Spec.Ports {
			serviceInfo.Ports[i] = ServicePort{
				Name:       port.Name,
				Port:       port.Port,
				Protocol:   string(port.Protocol),
				TargetPort: port.TargetPort.IntVal,
			}
		}

		key := fmt.Sprintf("%s/%s", svc.Namespace, svc.Name)
		e.serviceCache[key] = serviceInfo

		if svc.Spec.ClusterIP != "" {
			e.serviceCache[svc.Spec.ClusterIP] = serviceInfo
		}
	}

	e.logger.Infof("Synced %d services", len(services.Items))
	return nil
}

func (e *Enricher) GetPodByIP(ip string) *PodInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.podCache[ip]
}

func (e *Enricher) GetPodByName(namespace, name string) *PodInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()
	key := fmt.Sprintf("%s/%s", namespace, name)
	return e.podCache[key]
}

func (e *Enricher) GetServiceByIP(ip string) *ServiceInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.serviceCache[ip]
}

func (e *Enricher) GetServiceByName(namespace, name string) *ServiceInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()
	key := fmt.Sprintf("%s/%s", namespace, name)
	return e.serviceCache[key]
}

func (e *Enricher) EnrichWithMetadata(data map[string]interface{}) {
	hostIP, ok := data["host.ip"].(string)
	if !ok {
		if podIP, ok := data["pod.ip"].(string); ok {
			hostIP = podIP
		}
	}

	if hostIP == "" {
		return
	}

	if podInfo := e.GetPodByIP(hostIP); podInfo != nil {
		data["k8s.pod.name"] = podInfo.Name
		data["k8s.pod.uid"] = podInfo.UID
		data["k8s.pod.namespace"] = podInfo.Namespace
		data["k8s.pod.labels"] = podInfo.Labels
		data["k8s.pod.service_account"] = podInfo.ServiceAccount
		data["k8s.node.name"] = podInfo.NodeName
		data["k8s.container.name"] = podInfo.Containers[0].Name

		for _, ref := range podInfo.OwnerReferences {
			switch ref.Kind {
			case "Deployment":
				data["k8s.deployment.name"] = ref.Name
			case "DaemonSet":
				data["k8s.daemonset.name"] = ref.Name
			case "StatefulSet":
				data["k8s.statefulset.name"] = ref.Name
			case "ReplicaSet":
				data["k8s.replicaset.name"] = ref.Name
			}
		}
	}

	if nodeInfo := e.GetNodeByIP(hostIP); nodeInfo != nil {
		data["k8s.node.name"] = nodeInfo.Name
		data["k8s.node.labels"] = nodeInfo.Labels
	}

	if serviceInfo := e.GetServiceByIP(hostIP); serviceInfo != nil {
		data["k8s.service.name"] = serviceInfo.Name
		data["k8s.service.namespace"] = serviceInfo.Namespace
	}
}

func (e *Enricher) GetNodeByIP(ip string) *NodeInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, node := range e.nodeCache {
		if node.InternalIP == ip || node.ExternalIP == ip {
			return node
		}
	}
	return nil
}

func isInKubernetes() bool {
	_, err := os.Stat("/var/run/secrets/kubernetes.io")
	return err == nil
}

func GetLocalPodInfo() (string, string, error) {
	if !isInKubernetes() {
		return "", "", fmt.Errorf("not running in Kubernetes")
	}

	podName := os.Getenv("POD_NAME")
	namespace := os.Getenv("POD_NAMESPACE")

	if podName == "" {
		return "", "", fmt.Errorf("POD_NAME not set")
	}

	return podName, namespace, nil
}

func GetNodeName() (string, error) {
	if !isInKubernetes() {
		return "", fmt.Errorf("not running in Kubernetes")
	}

	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		return "", fmt.Errorf("NODE_NAME not set")
	}

	return nodeName, nil
}

func GetPodIPs() ([]string, error) {
	if !isInKubernetes() {
		return nil, fmt.Errorf("not running in Kubernetes")
	}

	hostIPs := os.Getenv("HOST_IPs")
	if hostIPs != "" {
		return strings.Split(hostIPs, ","), nil
	}

	hostIP := os.Getenv("HOST_IP")
	if hostIP != "" {
		return []string{hostIP}, nil
	}

	return nil, fmt.Errorf("no pod IPs found")
}

var strings = struct {
	Split func(string, string) []string
}{
	Split: func(s, sep string) []string {
		result := make([]string, 0)
		start := 0
		for i := 0; i < len(s); i++ {
			if i+len(sep) <= len(s) && s[i:i+len(sep)] == sep {
				result = append(result, s[start:i])
				start = i + len(sep)
			}
		}
		result = append(result, s[start:])
		return result
	},
}
