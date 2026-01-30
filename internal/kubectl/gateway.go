package kubectl

import (
	"context"
	"fmt"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Gateway represents a Gateway API Gateway resource
type Gateway struct {
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	GatewayClass string `json:"gateway_class"`
	External     bool   `json:"external"` // true if has external IP/hostname
}

// GatewayRoute represents a route that exposes a service via Gateway API
type GatewayRoute struct {
	Kind        string   `json:"kind"` // HTTPRoute, GRPCRoute, TCPRoute, TLSRoute
	Namespace   string   `json:"namespace"`
	Name        string   `json:"name"`
	GatewayName string   `json:"gateway_name"` // Referenced Gateway
	GatewayNS   string   `json:"gateway_namespace"`
	Hostnames   []string `json:"hostnames,omitempty"`
	BackendRefs []string `json:"backend_refs"` // Service names: "namespace/service"
}

// Gateway API GroupVersionResources
var (
	gatewayGVR = schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gateways",
	}
	httpRouteGVR = schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "httproutes",
	}
	grpcRouteGVR = schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "grpcroutes",
	}
	tcpRouteGVR = schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1alpha2",
		Resource: "tcproutes",
	}
	tlsRouteGVR = schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1alpha2",
		Resource: "tlsroutes",
	}
)

// ListGateways lists all Gateway API Gateways
func (c *Client) ListGateways(ctx context.Context) ([]Gateway, error) {
	list, err := c.dynamicClient.Resource(gatewayGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		// Gateway API might not be installed - return empty, not error
		return []Gateway{}, nil
	}

	var gateways []Gateway
	for _, item := range list.Items {
		ns := item.GetNamespace()
		name := item.GetName()

		// Get gatewayClassName
		spec, _ := item.Object["spec"].(map[string]interface{})
		gatewayClassName, _ := spec["gatewayClassName"].(string)

		// Check if external (has addresses with IP or Hostname)
		status, _ := item.Object["status"].(map[string]interface{})
		addresses, _ := status["addresses"].([]interface{})
		external := len(addresses) > 0

		gateways = append(gateways, Gateway{
			Namespace:    ns,
			Name:         name,
			GatewayClass: gatewayClassName,
			External:     external,
		})
	}

	return gateways, nil
}

// ListHTTPRoutes lists all HTTPRoutes
func (c *Client) ListHTTPRoutes(ctx context.Context) ([]GatewayRoute, error) {
	return c.listGatewayRoutes(ctx, httpRouteGVR, "HTTPRoute")
}

// ListGRPCRoutes lists all GRPCRoutes
func (c *Client) ListGRPCRoutes(ctx context.Context) ([]GatewayRoute, error) {
	return c.listGatewayRoutes(ctx, grpcRouteGVR, "GRPCRoute")
}

// ListTCPRoutes lists all TCPRoutes
func (c *Client) ListTCPRoutes(ctx context.Context) ([]GatewayRoute, error) {
	return c.listGatewayRoutes(ctx, tcpRouteGVR, "TCPRoute")
}

// ListTLSRoutes lists all TLSRoutes
func (c *Client) ListTLSRoutes(ctx context.Context) ([]GatewayRoute, error) {
	return c.listGatewayRoutes(ctx, tlsRouteGVR, "TLSRoute")
}

// listGatewayRoutes is a generic function to list Gateway API routes
func (c *Client) listGatewayRoutes(ctx context.Context, gvr schema.GroupVersionResource, kind string) ([]GatewayRoute, error) {
	list, err := c.dynamicClient.Resource(gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		// CRD might not exist - return empty
		return []GatewayRoute{}, nil
	}

	var routes []GatewayRoute
	for _, item := range list.Items {
		ns := item.GetNamespace()
		name := item.GetName()
		spec, _ := item.Object["spec"].(map[string]interface{})

		// Get parent gateway refs
		parentRefs, _ := spec["parentRefs"].([]interface{})
		var gatewayName, gatewayNS string
		if len(parentRefs) > 0 {
			parent, _ := parentRefs[0].(map[string]interface{})
			gatewayName, _ = parent["name"].(string)
			gatewayNS, _ = parent["namespace"].(string)
			if gatewayNS == "" {
				gatewayNS = ns // Default to same namespace
			}
		}

		// Get hostnames (HTTPRoute/GRPCRoute only)
		var hostnames []string
		if hs, ok := spec["hostnames"].([]interface{}); ok {
			for _, h := range hs {
				if hostname, ok := h.(string); ok {
					hostnames = append(hostnames, hostname)
				}
			}
		}

		// Get backend refs (services)
		var backendRefs []string
		rules, _ := spec["rules"].([]interface{})
		for _, rule := range rules {
			r, _ := rule.(map[string]interface{})
			backends, _ := r["backendRefs"].([]interface{})
			for _, backend := range backends {
				b, _ := backend.(map[string]interface{})
				svcName, _ := b["name"].(string)
				svcNS, _ := b["namespace"].(string)
				if svcNS == "" {
					svcNS = ns
				}
				if svcName != "" {
					backendRef := fmt.Sprintf("%s/%s", svcNS, svcName)
					// Avoid duplicates
					if !slices.Contains(backendRefs, backendRef) {
						backendRefs = append(backendRefs, backendRef)
					}
				}
			}
		}

		routes = append(routes, GatewayRoute{
			Kind:        kind,
			Namespace:   ns,
			Name:        name,
			GatewayName: gatewayName,
			GatewayNS:   gatewayNS,
			Hostnames:   hostnames,
			BackendRefs: backendRefs,
		})
	}

	return routes, nil
}

// ListAllGatewayRoutes returns all Gateway API routes (HTTP, gRPC, TCP, TLS)
func (c *Client) ListAllGatewayRoutes(ctx context.Context) ([]GatewayRoute, error) {
	var allRoutes []GatewayRoute

	httpRoutes, _ := c.ListHTTPRoutes(ctx)
	allRoutes = append(allRoutes, httpRoutes...)

	grpcRoutes, _ := c.ListGRPCRoutes(ctx)
	allRoutes = append(allRoutes, grpcRoutes...)

	tcpRoutes, _ := c.ListTCPRoutes(ctx)
	allRoutes = append(allRoutes, tcpRoutes...)

	tlsRoutes, _ := c.ListTLSRoutes(ctx)
	allRoutes = append(allRoutes, tlsRoutes...)

	return allRoutes, nil
}
