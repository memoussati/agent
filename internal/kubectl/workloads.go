package kubectl

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Workload represents a Kubernetes workload resource
type Workload struct {
	Kind                   string      `json:"kind"`
	Namespace              string      `json:"namespace"`
	Name                   string      `json:"name"`
	ServiceAccountName     string      `json:"service_account_name"`
	RunsAsNonRoot          *bool       `json:"runs_as_non_root"`          // nil if not configured, true/false otherwise
	HasSeccompProfile      bool        `json:"has_seccomp_profile"`       // true if any seccomp profile is set
	ReadOnlyRootFilesystem bool        `json:"read_only_root_filesystem"` // true if all containers have readOnlyRootFilesystem
	HasNetworkPolicy       bool        `json:"has_network_policy"`        // true if namespace has NetworkPolicy
	Containers             []Container `json:"containers"`
	ScanStatus             string      `json:"scan_status"` // scanned, pending, failed, unknown
}

// getServiceAccountName returns the service account name from a pod spec,
// defaulting to "default" if not specified
func getServiceAccountName(serviceAccountName string) string {
	if serviceAccountName != "" {
		return serviceAccountName
	}
	return "default"
}

// getRunsAsNonRoot checks if the pod is configured to run as non-root.
// Returns nil if not explicitly set, true/false otherwise.
func getRunsAsNonRoot(podSpec corev1.PodSpec) *bool {
	// Check pod-level security context first
	if podSpec.SecurityContext != nil && podSpec.SecurityContext.RunAsNonRoot != nil {
		return podSpec.SecurityContext.RunAsNonRoot
	}

	// Check if RunAsUser is explicitly set to 0 (root)
	if podSpec.SecurityContext != nil && podSpec.SecurityContext.RunAsUser != nil {
		isRoot := *podSpec.SecurityContext.RunAsUser == 0
		nonRoot := !isRoot
		return &nonRoot
	}

	// Check container-level (use first container as representative)
	for _, c := range podSpec.Containers {
		if c.SecurityContext != nil {
			if c.SecurityContext.RunAsNonRoot != nil {
				return c.SecurityContext.RunAsNonRoot
			}
			if c.SecurityContext.RunAsUser != nil {
				isRoot := *c.SecurityContext.RunAsUser == 0
				nonRoot := !isRoot
				return &nonRoot
			}
		}
	}

	return nil // Not explicitly configured
}

// hasSeccompProfile checks if any seccomp profile is configured
func hasSeccompProfile(podSpec corev1.PodSpec) bool {
	// Check pod-level
	if podSpec.SecurityContext != nil && podSpec.SecurityContext.SeccompProfile != nil {
		return true
	}

	// Check container-level
	for _, c := range podSpec.Containers {
		if c.SecurityContext != nil && c.SecurityContext.SeccompProfile != nil {
			return true
		}
	}

	return false
}

// hasReadOnlyRootFilesystem checks if all containers have read-only root filesystem
func hasReadOnlyRootFilesystem(podSpec corev1.PodSpec) bool {
	if len(podSpec.Containers) == 0 {
		return false
	}

	for _, c := range podSpec.Containers {
		if c.SecurityContext == nil ||
			c.SecurityContext.ReadOnlyRootFilesystem == nil ||
			!*c.SecurityContext.ReadOnlyRootFilesystem {
			return false
		}
	}

	return true
}

// Container represents a container within a workload
type Container struct {
	Name  string `json:"name"`
	Image string `json:"image"`
}

// ListDeployments lists all Deployments in a namespace
// If namespace is empty, lists across all namespaces
func (c *Client) ListDeployments(ctx context.Context, namespace string) ([]Workload, error) {
	deployments, err := c.clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list deployments: %w", err)
	}

	var workloads []Workload
	for _, dep := range deployments.Items {
		var containers []Container
		for _, c := range dep.Spec.Template.Spec.Containers {
			containers = append(containers, Container{
				Name:  c.Name,
				Image: c.Image,
			})
		}

		workloads = append(workloads, Workload{
			Kind:                   "Deployment",
			Namespace:              dep.Namespace,
			Name:                   dep.Name,
			ServiceAccountName:     getServiceAccountName(dep.Spec.Template.Spec.ServiceAccountName),
			RunsAsNonRoot:          getRunsAsNonRoot(dep.Spec.Template.Spec),
			HasSeccompProfile:      hasSeccompProfile(dep.Spec.Template.Spec),
			ReadOnlyRootFilesystem: hasReadOnlyRootFilesystem(dep.Spec.Template.Spec),
			HasNetworkPolicy:       false, // Set later based on namespace lookup
			Containers:             containers,
		})
	}
	return workloads, nil
}

// ListDaemonSets lists all DaemonSets in a namespace
func (c *Client) ListDaemonSets(ctx context.Context, namespace string) ([]Workload, error) {
	daemonsets, err := c.clientset.AppsV1().DaemonSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list daemonsets: %w", err)
	}

	var workloads []Workload
	for _, ds := range daemonsets.Items {
		var containers []Container
		for _, c := range ds.Spec.Template.Spec.Containers {
			containers = append(containers, Container{
				Name:  c.Name,
				Image: c.Image,
			})
		}

		workloads = append(workloads, Workload{
			Kind:                   "DaemonSet",
			Namespace:              ds.Namespace,
			Name:                   ds.Name,
			ServiceAccountName:     getServiceAccountName(ds.Spec.Template.Spec.ServiceAccountName),
			RunsAsNonRoot:          getRunsAsNonRoot(ds.Spec.Template.Spec),
			HasSeccompProfile:      hasSeccompProfile(ds.Spec.Template.Spec),
			ReadOnlyRootFilesystem: hasReadOnlyRootFilesystem(ds.Spec.Template.Spec),
			HasNetworkPolicy:       false, // Set later based on namespace lookup
			Containers:             containers,
		})
	}
	return workloads, nil
}

// ListStatefulSets lists all StatefulSets in a namespace
func (c *Client) ListStatefulSets(ctx context.Context, namespace string) ([]Workload, error) {
	statefulsets, err := c.clientset.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list statefulsets: %w", err)
	}

	var workloads []Workload
	for _, ss := range statefulsets.Items {
		var containers []Container
		for _, c := range ss.Spec.Template.Spec.Containers {
			containers = append(containers, Container{
				Name:  c.Name,
				Image: c.Image,
			})
		}

		workloads = append(workloads, Workload{
			Kind:                   "StatefulSet",
			Namespace:              ss.Namespace,
			Name:                   ss.Name,
			ServiceAccountName:     getServiceAccountName(ss.Spec.Template.Spec.ServiceAccountName),
			RunsAsNonRoot:          getRunsAsNonRoot(ss.Spec.Template.Spec),
			HasSeccompProfile:      hasSeccompProfile(ss.Spec.Template.Spec),
			ReadOnlyRootFilesystem: hasReadOnlyRootFilesystem(ss.Spec.Template.Spec),
			HasNetworkPolicy:       false, // Set later based on namespace lookup
			Containers:             containers,
		})
	}
	return workloads, nil
}

// ListCronJobs lists all CronJobs in a namespace
func (c *Client) ListCronJobs(ctx context.Context, namespace string) ([]Workload, error) {
	cronjobs, err := c.clientset.BatchV1().CronJobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list cronjobs: %w", err)
	}

	var workloads []Workload
	for _, cj := range cronjobs.Items {
		var containers []Container
		for _, c := range cj.Spec.JobTemplate.Spec.Template.Spec.Containers {
			containers = append(containers, Container{
				Name:  c.Name,
				Image: c.Image,
			})
		}

		workloads = append(workloads, Workload{
			Kind:                   "CronJob",
			Namespace:              cj.Namespace,
			Name:                   cj.Name,
			ServiceAccountName:     getServiceAccountName(cj.Spec.JobTemplate.Spec.Template.Spec.ServiceAccountName),
			RunsAsNonRoot:          getRunsAsNonRoot(cj.Spec.JobTemplate.Spec.Template.Spec),
			HasSeccompProfile:      hasSeccompProfile(cj.Spec.JobTemplate.Spec.Template.Spec),
			ReadOnlyRootFilesystem: hasReadOnlyRootFilesystem(cj.Spec.JobTemplate.Spec.Template.Spec),
			HasNetworkPolicy:       false, // Set later based on namespace lookup
			Containers:             containers,
		})
	}
	return workloads, nil
}

// ListJobs lists all Jobs in a namespace (excluding those owned by CronJobs or managed by Trivy)
func (c *Client) ListJobs(ctx context.Context, namespace string) ([]Workload, error) {
	jobs, err := c.clientset.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}

	var workloads []Workload
	for _, job := range jobs.Items {
		// Skip Jobs owned by CronJobs (they're tracked via CronJob)
		isOwnedByCronJob := false
		for _, ref := range job.OwnerReferences {
			if ref.Kind == "CronJob" {
				isOwnedByCronJob = true
				break
			}
		}
		if isOwnedByCronJob {
			continue
		}

		// Skip Trivy scan jobs (ephemeral jobs created by trivy-operator)
		if job.Labels["app.kubernetes.io/managed-by"] == "trivy-operator" {
			continue
		}

		var containers []Container
		for _, c := range job.Spec.Template.Spec.Containers {
			containers = append(containers, Container{
				Name:  c.Name,
				Image: c.Image,
			})
		}

		workloads = append(workloads, Workload{
			Kind:                   "Job",
			Namespace:              job.Namespace,
			Name:                   job.Name,
			ServiceAccountName:     getServiceAccountName(job.Spec.Template.Spec.ServiceAccountName),
			RunsAsNonRoot:          getRunsAsNonRoot(job.Spec.Template.Spec),
			HasSeccompProfile:      hasSeccompProfile(job.Spec.Template.Spec),
			ReadOnlyRootFilesystem: hasReadOnlyRootFilesystem(job.Spec.Template.Spec),
			HasNetworkPolicy:       false, // Set later based on namespace lookup
			Containers:             containers,
		})
	}
	return workloads, nil
}

// ListAllWorkloads lists all Deployments, DaemonSets, StatefulSets, CronJobs, and Jobs in a namespace
func (c *Client) ListAllWorkloads(ctx context.Context, namespace string) ([]Workload, error) {
	var allWorkloads []Workload

	deployments, err := c.ListDeployments(ctx, namespace)
	if err != nil {
		return nil, err
	}
	allWorkloads = append(allWorkloads, deployments...)

	daemonsets, err := c.ListDaemonSets(ctx, namespace)
	if err != nil {
		return nil, err
	}
	allWorkloads = append(allWorkloads, daemonsets...)

	statefulsets, err := c.ListStatefulSets(ctx, namespace)
	if err != nil {
		return nil, err
	}
	allWorkloads = append(allWorkloads, statefulsets...)

	cronjobs, err := c.ListCronJobs(ctx, namespace)
	if err != nil {
		return nil, err
	}
	allWorkloads = append(allWorkloads, cronjobs...)

	jobs, err := c.ListJobs(ctx, namespace)
	if err != nil {
		return nil, err
	}
	allWorkloads = append(allWorkloads, jobs...)

	return allWorkloads, nil
}

// GetNamespacesWithNetworkPolicy returns a set of namespaces that have at least one NetworkPolicy.
func (c *Client) GetNamespacesWithNetworkPolicy(ctx context.Context) (map[string]bool, error) {
	policies, err := c.clientset.NetworkingV1().NetworkPolicies("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list network policies: %w", err)
	}

	nsWithPolicy := make(map[string]bool)
	for _, np := range policies.Items {
		nsWithPolicy[np.Namespace] = true
	}

	return nsWithPolicy, nil
}
