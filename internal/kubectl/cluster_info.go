package kubectl

import (
	"context"
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterInfo contains detected cluster provider and platform information.
type ClusterInfo struct {
	Provider         string   `json:"provider"`           // eks, aks, gke, kapsule, openshift, k3s, k3d, rke, kubeadm, talos, k0s, kind, minikube, microk8s, orbstack, docker-desktop, unknown
	ControlPlaneType string   `json:"control_plane_type"` // managed, self-hosted
	KubeVersion      string   `json:"kube_version"`       // v1.29.1
	Platform         string   `json:"platform"`           // aws, azure, gcp, hetzner, digitalocean, linode, equinix, openstack, vsphere, scaleway, local, on-prem, unknown
	DetectedBy       []string `json:"detected_by"`        // which signals were used for detection
}

// DetectClusterInfo detects the cluster provider, platform, and configuration.
func (c *Client) DetectClusterInfo(ctx context.Context) (*ClusterInfo, error) {
	info := &ClusterInfo{
		Provider:         "unknown",
		ControlPlaneType: "unknown",
		Platform:         "unknown",
		DetectedBy:       []string{},
	}

	// Get Kubernetes version from server
	version, err := c.clientset.Discovery().ServerVersion()
	if err == nil {
		info.KubeVersion = version.GitVersion
		info.DetectedBy = append(info.DetectedBy, "server-version")
	}

	// Fetch nodes once and pass to detection functions
	nodes, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err == nil && len(nodes.Items) > 0 {
		// Detect managed cloud providers from node labels
		c.detectFromNodeLabels(nodes.Items, info)

		// Detect platform from ProviderID
		c.detectPlatformFromProviderID(nodes.Items, info)

		// Detect distribution from node info (OSImage, KubeletVersion, node name)
		if info.Provider == "unknown" {
			c.detectFromNodeInfo(nodes.Items, info)
		}
	}

	// Detect from kube-system pods if provider still unknown
	if info.Provider == "unknown" {
		_ = c.detectFromSystemPods(ctx, info)
	}

	// Detect real kubeadm via ConfigMap if provider still unknown
	if info.Provider == "unknown" {
		_ = c.detectKubeadm(ctx, info)
	}

	// Detect control plane type
	c.detectControlPlaneType(ctx, info)

	return info, nil
}

// detectFromNodeLabels checks node labels for managed cloud provider signals.
func (c *Client) detectFromNodeLabels(nodes []corev1.Node, info *ClusterInfo) {
	for _, node := range nodes {
		labels := node.Labels

		// EKS detection
		if _, ok := labels["eks.amazonaws.com/nodegroup"]; ok {
			info.Provider = "eks"
			info.ControlPlaneType = "managed"
			info.Platform = "aws"
			info.DetectedBy = append(info.DetectedBy, "node-label:eks.amazonaws.com/nodegroup")
			return
		}
		if _, ok := labels["alpha.eksctl.io/nodegroup-name"]; ok {
			info.Provider = "eks"
			info.ControlPlaneType = "managed"
			info.Platform = "aws"
			info.DetectedBy = append(info.DetectedBy, "node-label:alpha.eksctl.io/nodegroup-name")
			return
		}

		// AKS detection
		if _, ok := labels["kubernetes.azure.com/cluster"]; ok {
			info.Provider = "aks"
			info.ControlPlaneType = "managed"
			info.Platform = "azure"
			info.DetectedBy = append(info.DetectedBy, "node-label:kubernetes.azure.com/cluster")
			return
		}
		if _, ok := labels["kubernetes.azure.com/agentpool"]; ok {
			info.Provider = "aks"
			info.ControlPlaneType = "managed"
			info.Platform = "azure"
			info.DetectedBy = append(info.DetectedBy, "node-label:kubernetes.azure.com/agentpool")
			return
		}

		// GKE detection
		if _, ok := labels["cloud.google.com/gke-nodepool"]; ok {
			info.Provider = "gke"
			info.ControlPlaneType = "managed"
			info.Platform = "gcp"
			info.DetectedBy = append(info.DetectedBy, "node-label:cloud.google.com/gke-nodepool")
			return
		}

		// Scaleway Kapsule detection
		if _, ok := labels["k8s.scaleway.com/managed"]; ok {
			info.Provider = "kapsule"
			info.ControlPlaneType = "managed"
			info.Platform = "scaleway"
			info.DetectedBy = append(info.DetectedBy, "node-label:k8s.scaleway.com/managed")
			return
		}

		// OpenShift detection
		if _, ok := labels["node.openshift.io/os_id"]; ok {
			info.Provider = "openshift"
			info.ControlPlaneType = "self-hosted"
			info.DetectedBy = append(info.DetectedBy, "node-label:node.openshift.io/os_id")
			return
		}

		// Rancher RKE detection
		if _, ok := labels["rke.cattle.io/machine"]; ok {
			info.Provider = "rke"
			info.ControlPlaneType = "self-hosted"
			info.DetectedBy = append(info.DetectedBy, "node-label:rke.cattle.io/machine")
			return
		}

		// Talos detection via extension labels
		for labelKey := range labels {
			if strings.HasPrefix(labelKey, "extensions.talos.dev/") {
				info.Provider = "talos"
				info.ControlPlaneType = "self-hosted"
				info.DetectedBy = append(info.DetectedBy, "node-label:extensions.talos.dev")
				return
			}
		}

		// OrbStack detection via label
		if _, ok := labels["orb.dev/managed"]; ok {
			info.Provider = "orbstack"
			info.ControlPlaneType = "self-hosted"
			info.Platform = "local"
			info.DetectedBy = append(info.DetectedBy, "node-label:orb.dev/managed")
			return
		}
	}
}

// detectPlatformFromProviderID sets the platform based on node ProviderID prefix.
// This runs separately from provider detection so that both can be set
// (e.g., provider=talos + platform=hetzner).
func (c *Client) detectPlatformFromProviderID(nodes []corev1.Node, info *ClusterInfo) {
	// Skip if platform was already set by a managed provider detection
	if info.Platform != "unknown" {
		return
	}

	for _, node := range nodes {
		providerID := node.Spec.ProviderID
		if providerID == "" {
			continue
		}

		switch {
		case strings.HasPrefix(providerID, "aws://"):
			info.Platform = "aws"
			info.DetectedBy = append(info.DetectedBy, "node-providerid:aws")
		case strings.HasPrefix(providerID, "azure://"):
			info.Platform = "azure"
			info.DetectedBy = append(info.DetectedBy, "node-providerid:azure")
		case strings.HasPrefix(providerID, "gce://"):
			info.Platform = "gcp"
			info.DetectedBy = append(info.DetectedBy, "node-providerid:gce")
		case strings.HasPrefix(providerID, "hcloud://"):
			info.Platform = "hetzner"
			info.DetectedBy = append(info.DetectedBy, "node-providerid:hcloud")
		case strings.HasPrefix(providerID, "digitalocean://"):
			info.Platform = "digitalocean"
			info.DetectedBy = append(info.DetectedBy, "node-providerid:digitalocean")
		case strings.HasPrefix(providerID, "linode://"):
			info.Platform = "linode"
			info.DetectedBy = append(info.DetectedBy, "node-providerid:linode")
		case strings.HasPrefix(providerID, "equinixmetal://"):
			info.Platform = "equinix"
			info.DetectedBy = append(info.DetectedBy, "node-providerid:equinix")
		case strings.HasPrefix(providerID, "openstack://"):
			info.Platform = "openstack"
			info.DetectedBy = append(info.DetectedBy, "node-providerid:openstack")
		case strings.HasPrefix(providerID, "vsphere://"):
			info.Platform = "vsphere"
			info.DetectedBy = append(info.DetectedBy, "node-providerid:vsphere")
		case strings.HasPrefix(providerID, "kind://"):
			info.Platform = "local"
			info.DetectedBy = append(info.DetectedBy, "node-providerid:kind")
		}

		// Only need one node to determine platform
		if info.Platform != "unknown" {
			return
		}
	}
}

// detectFromNodeInfo checks node status fields for distribution signals.
func (c *Client) detectFromNodeInfo(nodes []corev1.Node, info *ClusterInfo) {
	for _, node := range nodes {
		nodeInfo := node.Status.NodeInfo

		// Talos detection via OSImage
		if strings.Contains(nodeInfo.OSImage, "Talos") {
			info.Provider = "talos"
			info.ControlPlaneType = "self-hosted"
			info.DetectedBy = append(info.DetectedBy, "node-osimage:talos")
			return
		}

		// k0s detection via KubeletVersion
		if strings.Contains(nodeInfo.KubeletVersion, "+k0s") {
			info.Provider = "k0s"
			info.ControlPlaneType = "self-hosted"
			info.DetectedBy = append(info.DetectedBy, "node-kubelet:k0s")
			return
		}

		// K3s detection via KubeletVersion (more reliable than pod name)
		if strings.Contains(nodeInfo.KubeletVersion, "+k3s") {
			info.Provider = "k3s"
			info.ControlPlaneType = "self-hosted"
			info.DetectedBy = append(info.DetectedBy, "node-kubelet:k3s")
			return
		}

		// Docker Desktop detection
		if node.Name == "docker-desktop" {
			info.Provider = "docker-desktop"
			info.ControlPlaneType = "self-hosted"
			info.Platform = "local"
			info.DetectedBy = append(info.DetectedBy, "node-name:docker-desktop")
			return
		}

		// OrbStack detection via node name
		if strings.Contains(node.Name, "orbstack") {
			info.Provider = "orbstack"
			info.ControlPlaneType = "self-hosted"
			info.Platform = "local"
			info.DetectedBy = append(info.DetectedBy, "node-name:orbstack")
			return
		}
	}
}

// detectFromSystemPods checks kube-system pods for provider signals.
func (c *Client) detectFromSystemPods(ctx context.Context, info *ClusterInfo) error {
	pods, err := c.clientset.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list kube-system pods: %w", err)
	}

	for _, pod := range pods.Items {
		name := pod.Name

		// K3s detection
		if strings.HasPrefix(name, "k3s-") || (strings.Contains(name, "traefik") && strings.Contains(name, "k3s")) {
			info.Provider = "k3s"
			info.ControlPlaneType = "self-hosted"
			info.DetectedBy = append(info.DetectedBy, "pod:k3s")
			return nil
		}

		// k3d detection (K3s in Docker)
		if strings.HasPrefix(name, "k3d-") {
			info.Provider = "k3d"
			info.ControlPlaneType = "self-hosted"
			info.Platform = "local"
			info.DetectedBy = append(info.DetectedBy, "pod:k3d")
			return nil
		}

		// MicroK8s detection
		if strings.HasPrefix(name, "microk8s-") || (strings.Contains(name, "calico-node") && strings.Contains(pod.Spec.NodeName, "microk8s")) {
			info.Provider = "microk8s"
			info.ControlPlaneType = "self-hosted"
			info.DetectedBy = append(info.DetectedBy, "pod:microk8s")
			return nil
		}

		// Kind detection
		if strings.HasPrefix(name, "kindnet-") {
			info.Provider = "kind"
			info.ControlPlaneType = "self-hosted"
			info.Platform = "local"
			info.DetectedBy = append(info.DetectedBy, "pod:kindnet")
			return nil
		}

		// Minikube detection
		if strings.HasPrefix(name, "storage-provisioner") && strings.Contains(pod.Spec.NodeName, "minikube") {
			info.Provider = "minikube"
			info.ControlPlaneType = "self-hosted"
			info.Platform = "local"
			info.DetectedBy = append(info.DetectedBy, "pod:minikube-storage-provisioner")
			return nil
		}
	}

	return nil
}

// detectKubeadm checks for the kubeadm-config ConfigMap which is only
// created by kubeadm init. This distinguishes real kubeadm clusters from
// other self-hosted distributions that also run kube-apiserver pods.
func (c *Client) detectKubeadm(ctx context.Context, info *ClusterInfo) error {
	configMaps, err := c.clientset.CoreV1().ConfigMaps("kube-system").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list kube-system configmaps: %w", err)
	}

	for _, cm := range configMaps.Items {
		if cm.Name == "kubeadm-config" {
			info.Provider = "kubeadm"
			info.ControlPlaneType = "self-hosted"
			info.DetectedBy = append(info.DetectedBy, "configmap:kubeadm-config")
			return nil
		}
	}

	return nil
}

// detectControlPlaneType determines if the control plane is managed or self-hosted.
func (c *Client) detectControlPlaneType(ctx context.Context, info *ClusterInfo) {
	// If already detected as managed, we're done
	if info.ControlPlaneType == "managed" {
		return
	}

	// Check for control plane components in kube-system
	pods, err := c.clientset.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{})
	if err != nil {
		return
	}

	controlPlaneComponents := []string{
		"kube-apiserver",
		"kube-controller-manager",
		"kube-scheduler",
		"etcd",
	}

	foundComponents := 0
	for _, pod := range pods.Items {
		for _, component := range controlPlaneComponents {
			if strings.HasPrefix(pod.Name, component) {
				foundComponents++
				break
			}
		}
	}

	// If we find at least 2 control plane components, it's self-hosted
	if foundComponents >= 2 {
		info.ControlPlaneType = "self-hosted"
		if !slices.Contains(info.DetectedBy, "control-plane-pods") {
			info.DetectedBy = append(info.DetectedBy, "control-plane-pods")
		}
	}
}
