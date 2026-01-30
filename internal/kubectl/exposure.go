package kubectl

import (
	"context"
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// WorkloadExposure represents network exposure analysis for a workload
type WorkloadExposure struct {
	// Workload identification (matches workload sync)
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"` // Deployment, DaemonSet, StatefulSet
	Name      string `json:"name"`

	// Exposure classification
	ExposureLevel string `json:"exposure_level"` // "internet", "cluster", "namespace", "isolated"

	// Service exposure
	Services []ServiceExposure `json:"services,omitempty"`

	// Ingress exposure
	Ingresses []IngressExposure `json:"ingresses,omitempty"`

	// Gateway API exposure
	GatewayRoutes []GatewayRouteExposure `json:"gateway_routes,omitempty"`

	// Network policy analysis
	HasNetworkPolicy     bool     `json:"has_network_policy"`
	IngressPolicyMode    string   `json:"ingress_policy_mode"` // "allow-all", "default-deny", "restricted"
	AllowedNamespaces    []string `json:"allowed_namespaces,omitempty"`
	AllowedExternalCIDRs []string `json:"allowed_external_cidrs,omitempty"`

	// Computed score (0.0 - 1.0, higher = more exposed)
	ExposureScore float64 `json:"exposure_score"`
}

// GatewayRouteExposure represents a Gateway API route exposing a workload
type GatewayRouteExposure struct {
	Kind        string   `json:"kind"` // HTTPRoute, GRPCRoute, TCPRoute, TLSRoute
	Name        string   `json:"name"`
	GatewayName string   `json:"gateway_name"`
	Hostnames   []string `json:"hostnames,omitempty"`
	External    bool     `json:"external"` // true if gateway is internet-facing
}

// ServiceExposure represents a service targeting a workload
type ServiceExposure struct {
	Name           string  `json:"name"`
	Type           string  `json:"type"` // ClusterIP, NodePort, LoadBalancer
	Ports          []int32 `json:"ports"`
	ExternalIP     string  `json:"external_ip,omitempty"`
	LoadBalancerIP string  `json:"loadbalancer_ip,omitempty"`
}

// IngressExposure represents an ingress route to a workload
type IngressExposure struct {
	Name      string   `json:"name"`
	Hosts     []string `json:"hosts"`
	Paths     []string `json:"paths"`
	TLS       bool     `json:"tls"`
	ClassName string   `json:"class_name,omitempty"`
}

// ListServices lists all Services in a namespace
func (c *Client) ListServices(ctx context.Context, namespace string) ([]corev1.Service, error) {
	services, err := c.clientset.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}
	return services.Items, nil
}

// ListIngresses lists all Ingresses in a namespace
func (c *Client) ListIngresses(ctx context.Context, namespace string) ([]networkingv1.Ingress, error) {
	ingresses, err := c.clientset.NetworkingV1().Ingresses(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list ingresses: %w", err)
	}
	return ingresses.Items, nil
}

// listNetworkPoliciesRaw lists all NetworkPolicies in a namespace (returns raw K8s objects)
func (c *Client) listNetworkPoliciesRaw(ctx context.Context, namespace string) ([]networkingv1.NetworkPolicy, error) {
	policies, err := c.clientset.NetworkingV1().NetworkPolicies(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list network policies: %w", err)
	}
	return policies.Items, nil
}

// AnalyzeWorkloadExposure analyzes the network exposure for a workload
func (c *Client) AnalyzeWorkloadExposure(ctx context.Context, workload Workload) (*WorkloadExposure, error) {
	exposure := &WorkloadExposure{
		Namespace:         workload.Namespace,
		Kind:              workload.Kind,
		Name:              workload.Name,
		IngressPolicyMode: "allow-all", // Default if no network policy
	}

	// Get workload labels for selector matching
	workloadLabels, err := c.getWorkloadLabels(ctx, workload)
	if err != nil {
		// Can't get labels, return with minimal analysis
		exposure.ExposureLevel = "cluster"
		exposure.ExposureScore = 0.5
		return exposure, nil
	}

	// Analyze services targeting this workload
	services, err := c.ListServices(ctx, workload.Namespace)
	if err == nil {
		exposure.Services = c.findServicesForWorkload(services, workloadLabels)
	}

	// Analyze ingresses routing to services of this workload
	ingresses, err := c.ListIngresses(ctx, workload.Namespace)
	if err == nil {
		exposure.Ingresses = c.findIngressesForWorkload(ingresses, exposure.Services)
	}

	// Analyze Gateway API routes targeting services of this workload
	gateways, _ := c.ListGateways(ctx)
	gatewayRoutes, _ := c.ListAllGatewayRoutes(ctx)
	exposure.GatewayRoutes = c.findGatewayRoutesForWorkload(gatewayRoutes, gateways, exposure.Services, workload.Namespace)

	// Analyze network policies affecting this workload
	policies, err := c.listNetworkPoliciesRaw(ctx, workload.Namespace)
	if err == nil {
		c.analyzeNetworkPolicies(exposure, policies, workloadLabels)
	}

	// Classify exposure level
	exposure.ExposureLevel = classifyExposure(exposure)

	// Calculate exposure score
	exposure.ExposureScore = calculateExposureScore(exposure)

	return exposure, nil
}

// getWorkloadLabels retrieves the pod template labels for a workload
func (c *Client) getWorkloadLabels(ctx context.Context, workload Workload) (map[string]string, error) {
	switch workload.Kind {
	case "Deployment":
		dep, err := c.clientset.AppsV1().Deployments(workload.Namespace).Get(ctx, workload.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return dep.Spec.Template.Labels, nil

	case "DaemonSet":
		ds, err := c.clientset.AppsV1().DaemonSets(workload.Namespace).Get(ctx, workload.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return ds.Spec.Template.Labels, nil

	case "StatefulSet":
		ss, err := c.clientset.AppsV1().StatefulSets(workload.Namespace).Get(ctx, workload.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return ss.Spec.Template.Labels, nil

	case "CronJob":
		cj, err := c.clientset.BatchV1().CronJobs(workload.Namespace).Get(ctx, workload.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return cj.Spec.JobTemplate.Spec.Template.Labels, nil

	case "Job":
		job, err := c.clientset.BatchV1().Jobs(workload.Namespace).Get(ctx, workload.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return job.Spec.Template.Labels, nil

	default:
		return nil, fmt.Errorf("unsupported workload kind: %s", workload.Kind)
	}
}

// findServicesForWorkload finds services that select pods from this workload
func (c *Client) findServicesForWorkload(services []corev1.Service, workloadLabels map[string]string) []ServiceExposure {
	var result []ServiceExposure

	if len(workloadLabels) == 0 {
		return result
	}

	workloadLabelSet := labels.Set(workloadLabels)

	for _, svc := range services {
		// Skip services without selectors (ExternalName, headless)
		if len(svc.Spec.Selector) == 0 {
			continue
		}

		// Check if service selector matches workload labels
		selector := labels.SelectorFromSet(svc.Spec.Selector)
		if !selector.Matches(workloadLabelSet) {
			continue
		}

		// Extract service exposure info
		svcExposure := ServiceExposure{
			Name:  svc.Name,
			Type:  string(svc.Spec.Type),
			Ports: make([]int32, 0, len(svc.Spec.Ports)),
		}

		for _, port := range svc.Spec.Ports {
			svcExposure.Ports = append(svcExposure.Ports, port.Port)
		}

		// Check for external IPs
		if len(svc.Spec.ExternalIPs) > 0 {
			svcExposure.ExternalIP = svc.Spec.ExternalIPs[0]
		}

		// Check for LoadBalancer IP
		if svc.Spec.Type == corev1.ServiceTypeLoadBalancer {
			if len(svc.Status.LoadBalancer.Ingress) > 0 {
				ing := svc.Status.LoadBalancer.Ingress[0]
				if ing.IP != "" {
					svcExposure.LoadBalancerIP = ing.IP
				} else if ing.Hostname != "" {
					svcExposure.LoadBalancerIP = ing.Hostname
				}
			}
		}

		result = append(result, svcExposure)
	}

	return result
}

// findGatewayRoutesForWorkload finds Gateway API routes that target services of this workload
func (c *Client) findGatewayRoutesForWorkload(routes []GatewayRoute, gateways []Gateway, workloadServices []ServiceExposure, workloadNS string) []GatewayRouteExposure {
	var result []GatewayRouteExposure

	// Build map of gateways for quick lookup
	gatewayMap := make(map[string]Gateway)
	for _, gw := range gateways {
		key := fmt.Sprintf("%s/%s", gw.Namespace, gw.Name)
		gatewayMap[key] = gw
	}

	// Build set of service names for quick lookup
	svcRefs := make(map[string]bool)
	for _, svc := range workloadServices {
		// Routes reference services as "namespace/name"
		svcRefs[fmt.Sprintf("%s/%s", workloadNS, svc.Name)] = true
	}

	if len(svcRefs) == 0 {
		return result
	}

	for _, route := range routes {
		// Check if route references any of our services
		referencesWorkload := false
		for _, backendRef := range route.BackendRefs {
			if svcRefs[backendRef] {
				referencesWorkload = true
				break
			}
		}

		if !referencesWorkload {
			continue
		}

		// Check if the gateway is external
		gwKey := fmt.Sprintf("%s/%s", route.GatewayNS, route.GatewayName)
		gw, exists := gatewayMap[gwKey]
		external := exists && gw.External

		result = append(result, GatewayRouteExposure{
			Kind:        route.Kind,
			Name:        route.Name,
			GatewayName: route.GatewayName,
			Hostnames:   route.Hostnames,
			External:    external,
		})
	}

	return result
}

// findIngressesForWorkload finds ingresses that route to services of this workload
func (c *Client) findIngressesForWorkload(ingresses []networkingv1.Ingress, workloadServices []ServiceExposure) []IngressExposure {
	var result []IngressExposure

	// Build set of service names for quick lookup
	svcNames := make(map[string]bool)
	for _, svc := range workloadServices {
		svcNames[svc.Name] = true
	}

	if len(svcNames) == 0 {
		return result
	}

	for _, ing := range ingresses {
		// Check if any rule references a service from our workload
		referencesWorkload := false
		var hosts []string
		var paths []string

		for _, rule := range ing.Spec.Rules {
			if rule.HTTP == nil {
				continue
			}

			for _, path := range rule.HTTP.Paths {
				if path.Backend.Service != nil && svcNames[path.Backend.Service.Name] {
					referencesWorkload = true
					if rule.Host != "" && !slices.Contains(hosts, rule.Host) {
						hosts = append(hosts, rule.Host)
					}
					if path.Path != "" && !slices.Contains(paths, path.Path) {
						paths = append(paths, path.Path)
					}
				}
			}
		}

		// Check default backend
		if ing.Spec.DefaultBackend != nil && ing.Spec.DefaultBackend.Service != nil {
			if svcNames[ing.Spec.DefaultBackend.Service.Name] {
				referencesWorkload = true
			}
		}

		if !referencesWorkload {
			continue
		}

		ingExposure := IngressExposure{
			Name:  ing.Name,
			Hosts: hosts,
			Paths: paths,
			TLS:   len(ing.Spec.TLS) > 0,
		}

		if ing.Spec.IngressClassName != nil {
			ingExposure.ClassName = *ing.Spec.IngressClassName
		}

		result = append(result, ingExposure)
	}

	return result
}

// analyzeNetworkPolicies analyzes network policies affecting the workload
func (c *Client) analyzeNetworkPolicies(exposure *WorkloadExposure, policies []networkingv1.NetworkPolicy, workloadLabels map[string]string) {
	if len(workloadLabels) == 0 {
		return
	}

	workloadLabelSet := labels.Set(workloadLabels)
	var applicablePolicies []networkingv1.NetworkPolicy

	for _, policy := range policies {
		// Check if policy selector matches workload labels
		selector, err := metav1.LabelSelectorAsSelector(&policy.Spec.PodSelector)
		if err != nil {
			continue
		}

		// Empty selector matches all pods in namespace
		if selector.Empty() || selector.Matches(workloadLabelSet) {
			applicablePolicies = append(applicablePolicies, policy)
		}
	}

	if len(applicablePolicies) == 0 {
		exposure.HasNetworkPolicy = false
		exposure.IngressPolicyMode = "allow-all"
		return
	}

	exposure.HasNetworkPolicy = true

	// Analyze ingress rules
	hasIngressRules := false
	allowsAllIngress := false
	allowedNamespaces := make(map[string]bool)
	var allowedCIDRs []string

	for _, policy := range applicablePolicies {
		// Check if policy has ingress rules
		for _, policyType := range policy.Spec.PolicyTypes {
			if policyType == networkingv1.PolicyTypeIngress {
				hasIngressRules = true
			}
		}

		for _, rule := range policy.Spec.Ingress {
			// Empty from means allow all
			if len(rule.From) == 0 {
				allowsAllIngress = true
				continue
			}

			for _, from := range rule.From {
				// Namespace selector
				if from.NamespaceSelector != nil {
					if from.NamespaceSelector.MatchLabels == nil && len(from.NamespaceSelector.MatchExpressions) == 0 {
						// Empty selector = all namespaces
						allowsAllIngress = true
					} else {
						// Specific namespaces - we'd need to resolve which ones
						// For now, mark as restricted
						allowedNamespaces["<restricted>"] = true
					}
				}

				// Pod selector in same namespace
				if from.PodSelector != nil && from.NamespaceSelector == nil {
					allowedNamespaces[exposure.Namespace] = true
				}

				// IP block (external access)
				if from.IPBlock != nil {
					if from.IPBlock.CIDR == "0.0.0.0/0" || from.IPBlock.CIDR == "::/0" {
						allowsAllIngress = true
					} else {
						allowedCIDRs = append(allowedCIDRs, from.IPBlock.CIDR)
					}
				}
			}
		}
	}

	// Determine ingress policy mode
	if !hasIngressRules {
		// No ingress policy type = allow all ingress
		exposure.IngressPolicyMode = "allow-all"
	} else if allowsAllIngress {
		exposure.IngressPolicyMode = "allow-all"
	} else if len(allowedNamespaces) > 0 || len(allowedCIDRs) > 0 {
		exposure.IngressPolicyMode = "restricted"
	} else {
		exposure.IngressPolicyMode = "default-deny"
	}

	// Collect allowed namespaces
	for ns := range allowedNamespaces {
		exposure.AllowedNamespaces = append(exposure.AllowedNamespaces, ns)
	}
	exposure.AllowedExternalCIDRs = allowedCIDRs
}

// classifyExposure determines the exposure level based on analysis
func classifyExposure(exposure *WorkloadExposure) string {
	// Level 1: Internet-facing (Ingress, Gateway API route to external gateway, or LoadBalancer/NodePort)
	if len(exposure.Ingresses) > 0 {
		return "internet"
	}

	// Check Gateway API routes - if any route points to an external gateway
	for _, route := range exposure.GatewayRoutes {
		if route.External {
			return "internet"
		}
	}

	for _, svc := range exposure.Services {
		if svc.Type == "LoadBalancer" || svc.Type == "NodePort" {
			return "internet"
		}
	}

	// Level 2: Cluster-wide accessible (no NetworkPolicy or allow-all)
	if !exposure.HasNetworkPolicy || exposure.IngressPolicyMode == "allow-all" {
		return "cluster"
	}

	// Level 3: Namespace-restricted (NetworkPolicy limits to same namespace)
	if exposure.IngressPolicyMode == "restricted" {
		// Check if only same namespace is allowed
		if len(exposure.AllowedNamespaces) == 1 && exposure.AllowedNamespaces[0] == exposure.Namespace {
			return "namespace"
		}
		// Has some restrictions but not fully isolated
		return "namespace"
	}

	// Level 4: Isolated (strict NetworkPolicy, minimal access)
	return "isolated"
}

// CVSS v3.1 Attack Vector metric values
// See: https://www.first.org/cvss/v3.1/specification-document#2-1-1-Attack-Vector-AV
const (
	// CVSSAttackVectorNetwork - The vulnerable component is bound to the network stack
	// and the set of possible attackers extends beyond the other options, up to and
	// including the entire Internet.
	CVSSAttackVectorNetwork = 0.85

	// CVSSAttackVectorAdjacent - The vulnerable component is bound to the network stack,
	// but the attack is limited at the protocol level to a logically adjacent topology.
	// This can mean an attack must be launched from the same shared physical or logical network.
	CVSSAttackVectorAdjacent = 0.62

	// CVSSAttackVectorLocal - The vulnerable component is not bound to the network stack
	// and the attacker's path is via read/write/execute capabilities.
	CVSSAttackVectorLocal = 0.55

	// CVSSAttackVectorPhysical - The attack requires the attacker to physically touch
	// or manipulate the vulnerable component.
	CVSSAttackVectorPhysical = 0.20
)

// calculateExposureScore calculates a numeric exposure score based on CVSS v3.1 Attack Vector.
//
// Mapping to CVSS Attack Vector:
//   - internet → Network (0.85): Remotely exploitable from the internet
//   - cluster  → Adjacent (0.62): Requires access to cluster network
//   - namespace → Local (0.55): Requires access within the same namespace
//   - isolated → Physical (0.20): Requires bypassing strict network policies
//
// Reference: https://www.first.org/cvss/v3.1/specification-document#2-1-1-Attack-Vector-AV
func calculateExposureScore(exposure *WorkloadExposure) float64 {
	// Map exposure level to CVSS Attack Vector
	switch exposure.ExposureLevel {
	case "internet":
		return CVSSAttackVectorNetwork
	case "cluster":
		return CVSSAttackVectorAdjacent
	case "namespace":
		return CVSSAttackVectorLocal
	case "isolated":
		return CVSSAttackVectorPhysical
	default:
		// Unknown exposure level, assume adjacent (cluster-wide) access
		return CVSSAttackVectorAdjacent
	}
}

// GetExposedPorts extracts all exposed ports from services
func (e *WorkloadExposure) GetExposedPorts() []int32 {
	portSet := make(map[int32]bool)
	for _, svc := range e.Services {
		for _, port := range svc.Ports {
			portSet[port] = true
		}
	}

	var ports []int32
	for port := range portSet {
		ports = append(ports, port)
	}
	return ports
}

// GetIngressHosts extracts all ingress hosts
func (e *WorkloadExposure) GetIngressHosts() []string {
	hostSet := make(map[string]bool)
	for _, ing := range e.Ingresses {
		for _, host := range ing.Hosts {
			hostSet[host] = true
		}
	}

	var hosts []string
	for host := range hostSet {
		hosts = append(hosts, host)
	}
	return hosts
}

// ExposureLevelLabel returns a human-readable label for the exposure level
func ExposureLevelLabel(level string) string {
	switch level {
	case "internet":
		return "Internet-exposed"
	case "cluster":
		return "Cluster-accessible"
	case "namespace":
		return "Namespace-only"
	case "isolated":
		return "Isolated"
	default:
		return "Unknown"
	}
}

// String returns a string representation of the exposure
func (e *WorkloadExposure) String() string {
	return fmt.Sprintf("%s/%s/%s: %s (score: %.2f)",
		e.Namespace, e.Kind, e.Name,
		ExposureLevelLabel(e.ExposureLevel),
		e.ExposureScore)
}

// HasExternalAccess returns true if the workload can be accessed from outside the cluster
func (e *WorkloadExposure) HasExternalAccess() bool {
	if len(e.Ingresses) > 0 {
		return true
	}

	// Check Gateway API routes to external gateways
	for _, route := range e.GatewayRoutes {
		if route.External {
			return true
		}
	}

	for _, svc := range e.Services {
		if svc.Type == "LoadBalancer" || svc.Type == "NodePort" {
			return true
		}
		if svc.ExternalIP != "" {
			return true
		}
	}
	return false
}

// ServiceTypeLabel returns a human-readable label for service types
func ServiceTypeLabel(svcType string) string {
	switch svcType {
	case "LoadBalancer":
		return "Load Balancer"
	case "NodePort":
		return "Node Port"
	case "ClusterIP":
		return "Cluster IP"
	case "ExternalName":
		return "External Name"
	default:
		return svcType
	}
}

// FormatPorts formats port numbers as a comma-separated string
func FormatPorts(ports []int32) string {
	if len(ports) == 0 {
		return ""
	}
	strs := make([]string, len(ports))
	for i, p := range ports {
		strs[i] = fmt.Sprintf("%d", p)
	}
	return strings.Join(strs, ", ")
}
