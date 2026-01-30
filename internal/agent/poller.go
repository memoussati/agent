package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/trixsec-dev/trix-agent/internal/kubectl"
	"github.com/trixsec-dev/trix-agent/internal/trivy"
)

// VulnerabilityEvent represents a change in vulnerability state.
type VulnerabilityEvent struct {
	ID              string     `json:"ID"`
	Type            string     `json:"Type"` // NEW, FIXED
	CVE             string     `json:"CVE"`
	Workload        string     `json:"Workload"`
	Severity        string     `json:"Severity"`
	Image           string     `json:"Image"` // package:version (legacy)
	ContainerName   string     `json:"ContainerName,omitempty"`
	ImageRepository string     `json:"ImageRepository,omitempty"`
	ImageTag        string     `json:"ImageTag,omitempty"`
	ImageDigest     string     `json:"ImageDigest,omitempty"`
	FixedVersion    string     `json:"FixedVersion,omitempty"` // version that fixes this vulnerability
	FirstSeen       time.Time  `json:"FirstSeen"`
	FixedAt         *time.Time `json:"FixedAt,omitempty"`
}

// ComplianceEvent represents a compliance check result from CIS/NSA/PSS benchmarks.
// This is the legacy summary format, kept for backwards compatibility.
type ComplianceEvent struct {
	Framework   string `json:"Framework"`   // k8s-cis-1.23, k8s-nsa-1.0, etc.
	ControlID   string `json:"ControlID"`   // 1.1.1, 1.2.3, etc.
	ControlName string `json:"ControlName"` // Human readable name
	Severity    string `json:"Severity"`    // CRITICAL, HIGH, MEDIUM, LOW
	Status      string `json:"Status"`      // pass, fail
	TotalFail   int    `json:"TotalFail"`   // Number of failed checks
}

// DetailedComplianceCheck represents a single compliance check failure with full details.
// Sent for each failing check on each resource - enables drill-down in UI.
type DetailedComplianceCheck struct {
	// Check identification
	CheckID  string `json:"CheckID"`  // KSV001, AVD-KCV-0048, etc.
	Category string `json:"Category"` // Kubernetes Security Check, etc.

	// Resource where check failed
	ResourceKind      string `json:"ResourceKind"`      // DaemonSet, Deployment, Pod, etc.
	ResourceNamespace string `json:"ResourceNamespace"` // kube-system, default, etc.
	ResourceName      string `json:"ResourceName"`      // cilium, coredns, etc.

	// Check details
	Title       string   `json:"Title"`
	Description string   `json:"Description"`
	Severity    string   `json:"Severity"` // CRITICAL, HIGH, MEDIUM, LOW
	Messages    []string `json:"Messages"` // Specific failure reasons per container
	Remediation string   `json:"Remediation"`

	// Status
	Success bool `json:"Success"` // false for failures
}

// Poller periodically scans Trivy CRDs and detects changes.
type Poller struct {
	trivyClient *trivy.Client
	db          *DB
	config      *Config
	logger      *slog.Logger
}

// NewPoller creates a new Trivy CRD poller.
func NewPoller(db *DB, config *Config, logger *slog.Logger) (*Poller, error) {
	k8sClient, err := kubectl.NewClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s client: %w", err)
	}

	trivyClient := trivy.NewClient(k8sClient)

	return &Poller{
		trivyClient: trivyClient,
		db:          db,
		config:      config,
		logger:      logger,
	}, nil
}

// Poll performs a single poll of Trivy CRDs and returns events.
func (p *Poller) Poll(ctx context.Context) ([]VulnerabilityEvent, error) {
	p.logger.Info("starting poll")

	// Get all findings from Trivy
	findings, err := p.getFindings(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get findings: %w", err)
	}

	p.logger.Info("found vulnerabilities", "count", len(findings))

	var events []VulnerabilityEvent
	var currentIDs []string

	// Track current digests per workload+container for MarkFixed logic
	currentDigests := make(map[DigestKey]string)

	// Process each finding
	for _, f := range findings {
		record := p.findingToRecord(f)
		currentIDs = append(currentIDs, record.ID)

		// Track the current digest for this workload+container
		// This is used by MarkFixed to determine if image was actually updated
		if record.ImageDigest != "" {
			key := DigestKey{Workload: record.Workload, ContainerName: record.ContainerName}
			currentDigests[key] = record.ImageDigest
		}

		isNew, err := p.db.UpsertVulnerability(ctx, record)
		if err != nil {
			p.logger.Error("failed to upsert vulnerability", "id", record.ID, "error", err)
			continue
		}

		if isNew {
			events = append(events, VulnerabilityEvent{
				ID:              record.ID,
				Type:            "NEW",
				CVE:             record.CVE,
				Workload:        record.Workload,
				Severity:        record.Severity,
				Image:           record.Image,
				ContainerName:   record.ContainerName,
				ImageRepository: record.ImageRepository,
				ImageTag:        record.ImageTag,
				ImageDigest:     record.ImageDigest,
				FixedVersion:    record.FixedVersion,
				FirstSeen:       time.Now(),
			})
		}
	}

	// Mark vulnerabilities not in current scan as fixed (only if digest changed)
	fixed, err := p.db.MarkFixed(ctx, currentIDs, currentDigests)
	if err != nil {
		p.logger.Error("failed to mark fixed vulnerabilities", "error", err)
	} else {
		for _, v := range fixed {
			events = append(events, VulnerabilityEvent{
				ID:              v.ID,
				Type:            "FIXED",
				CVE:             v.CVE,
				Workload:        v.Workload,
				Severity:        v.Severity,
				Image:           v.Image,
				ContainerName:   v.ContainerName,
				ImageRepository: v.ImageRepository,
				ImageTag:        v.ImageTag,
				ImageDigest:     v.ImageDigest,
				FixedVersion:    v.FixedVersion,
				FirstSeen:       v.FirstSeen,
				FixedAt:         v.FixedAt,
			})
		}
	}

	p.logger.Info("poll complete", "new", countByType(events, "NEW"), "fixed", countByType(events, "FIXED"))

	return events, nil
}

// getFindings retrieves all vulnerability findings from Trivy CRDs.
func (p *Poller) getFindings(ctx context.Context) ([]trivy.Finding, error) {
	// Use the existing scanner infrastructure
	scanners := []trivy.Scanner{
		trivy.NewTrivyVulnScanner(p.trivyClient),
		trivy.NewClusterVulnScanner(p.trivyClient),
	}

	var allFindings []trivy.Finding

	for _, scanner := range scanners {
		if len(p.config.Namespaces) > 0 {
			// Scan specific namespaces
			for _, ns := range p.config.Namespaces {
				findings, err := scanner.Scan(ctx, ns)
				if err != nil {
					p.logger.Warn("scanner failed", "scanner", scanner.Name(), "namespace", ns, "error", err)
					continue
				}
				allFindings = append(allFindings, findings...)
			}
		} else {
			// Scan all namespaces
			findings, err := scanner.Scan(ctx, "")
			if err != nil {
				p.logger.Warn("scanner failed", "scanner", scanner.Name(), "error", err)
				continue
			}
			allFindings = append(allFindings, findings...)
		}
	}

	// Filter to only vulnerability type
	var vulnFindings []trivy.Finding
	for _, f := range allFindings {
		if f.Type == trivy.FindingTypeVulnerability {
			vulnFindings = append(vulnFindings, f)
		}
	}

	return vulnFindings, nil
}

// findingToRecord converts a Trivy finding to a database record.
func (p *Poller) findingToRecord(f trivy.Finding) *VulnerabilityRecord {
	workload := fmt.Sprintf("%s/%s/%s", f.Namespace, f.ResourceKind, f.ResourceName)

	// Extract package info from raw data
	pkgName := ""
	pkgVersion := ""
	fixedVersion := ""
	if raw, ok := f.RawData.(trivy.Vulnerability); ok {
		pkgName = raw.PkgName
		pkgVersion = raw.InstalledVersion
		fixedVersion = raw.FixedVersion
	}

	// Create unique ID from CVE + workload + package + container
	// Including container ensures same CVE in different containers of same workload are tracked separately
	idHash := sha256.Sum256([]byte(f.ID + workload + pkgName + f.ContainerName))
	id := fmt.Sprintf("%x", idHash[:8])

	image := ""
	if pkgName != "" {
		image = fmt.Sprintf("%s:%s", pkgName, pkgVersion)
	}

	return &VulnerabilityRecord{
		ID:              id,
		CVE:             f.ID,
		Workload:        workload,
		Severity:        string(f.Severity),
		Image:           image,
		ContainerName:   f.ContainerName,
		ImageRepository: f.ImageRepository,
		ImageTag:        f.ImageTag,
		ImageDigest:     f.ImageDigest,
		FixedVersion:    fixedVersion,
		State:           StateOpen,
	}
}

func countByType(events []VulnerabilityEvent, eventType string) int {
	count := 0
	for _, e := range events {
		if e.Type == eventType {
			count++
		}
	}
	return count
}

// PollCompliance fetches CIS/NSA/PSS benchmark results from Trivy ClusterComplianceReports.
// Returns summary data for backwards compatibility.
func (p *Poller) PollCompliance(ctx context.Context) ([]ComplianceEvent, error) {
	p.logger.Info("polling compliance benchmarks")

	reports, err := p.trivyClient.ListBenchmarkReports(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list benchmark reports: %w", err)
	}

	var events []ComplianceEvent

	for _, report := range reports {
		framework, controls, err := p.trivyClient.ParseBenchmarkControls(report)
		if err != nil {
			p.logger.Warn("failed to parse benchmark report", "error", err)
			continue
		}

		for _, ctrl := range controls {
			status := "pass"
			if ctrl.TotalFail > 0 {
				status = "fail"
			}

			events = append(events, ComplianceEvent{
				Framework:   framework,
				ControlID:   ctrl.ID,
				ControlName: ctrl.Name,
				Severity:    ctrl.Severity,
				Status:      status,
				TotalFail:   ctrl.TotalFail,
			})
		}

		p.logger.Info("parsed benchmark", "framework", framework, "controls", len(controls))
	}

	p.logger.Info("compliance poll complete", "frameworks", len(reports), "total_controls", len(events))
	return events, nil
}

// PollDetailedCompliance fetches ConfigAuditReports with full check details.
// Returns ALL checks (pass and fail) for stateless sync - SAAS determines new/fixed.
func (p *Poller) PollDetailedCompliance(ctx context.Context) ([]DetailedComplianceCheck, error) {
	p.logger.Info("polling detailed compliance checks")

	// Fetch all ConfigAuditReports from all namespaces
	reports, err := p.trivyClient.ListConfigAuditReports(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list config audit reports: %w", err)
	}

	var checks []DetailedComplianceCheck

	for _, report := range reports {
		// Extract resource info from labels
		metadata, ok := report["metadata"].(map[string]interface{})
		if !ok {
			continue
		}

		labels, _ := metadata["labels"].(map[string]interface{})
		resourceKind := getStringFromMap(labels, "trivy-operator.resource.kind")
		resourceName := getStringFromMap(labels, "trivy-operator.resource.name")
		resourceNamespace := getStringFromMap(labels, "trivy-operator.resource.namespace")

		// Parse the checks
		parsedChecks, err := p.trivyClient.ParseComplianceChecks(report)
		if err != nil {
			continue
		}

		for _, c := range parsedChecks {
			// Only include failed checks to reduce payload size
			if c.Success {
				continue
			}

			checks = append(checks, DetailedComplianceCheck{
				CheckID:           c.CheckID,
				Category:          c.Category,
				ResourceKind:      resourceKind,
				ResourceNamespace: resourceNamespace,
				ResourceName:      resourceName,
				Title:             c.Title,
				Description:       c.Description,
				Severity:          c.Severity,
				Messages:          c.Messages,
				Remediation:       c.Remediation,
				Success:           c.Success,
			})
		}
	}

	p.logger.Info("detailed compliance poll complete", "reports", len(reports), "failed_checks", len(checks))
	return checks, nil
}

func getStringFromMap(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// PollWorkloads fetches all workloads (Deployments, DaemonSets, StatefulSets) from K8s API.
// Returns workload inventory with scan status for syncing to SAAS.
func (p *Poller) PollWorkloads(ctx context.Context) ([]kubectl.Workload, error) {
	p.logger.Info("polling workload inventory")

	var allWorkloads []kubectl.Workload
	k8sClient := p.trivyClient.K8sClient()

	if len(p.config.Namespaces) > 0 {
		// Poll specific namespaces
		for _, ns := range p.config.Namespaces {
			workloads, err := k8sClient.ListAllWorkloads(ctx, ns)
			if err != nil {
				p.logger.Warn("failed to list workloads", "namespace", ns, "error", err)
				continue
			}
			allWorkloads = append(allWorkloads, workloads...)
		}
	} else {
		// Poll all namespaces
		workloads, err := k8sClient.ListAllWorkloads(ctx, "")
		if err != nil {
			return nil, fmt.Errorf("failed to list workloads: %w", err)
		}
		allWorkloads = workloads
	}

	// Get scanned workload set from VulnerabilityReports
	scannedSet := p.getScannedWorkloadSet(ctx)

	// Get namespaces with network policies for hardening status
	nsWithPolicy, err := k8sClient.GetNamespacesWithNetworkPolicy(ctx)
	if err != nil {
		p.logger.Warn("failed to get network policies", "error", err)
		nsWithPolicy = make(map[string]bool)
	}

	// Correlate workloads with scan status and network policy
	for i := range allWorkloads {
		key := fmt.Sprintf("%s/%s/%s", allWorkloads[i].Namespace, allWorkloads[i].Kind, allWorkloads[i].Name)
		if scannedSet[key] {
			allWorkloads[i].ScanStatus = "scanned"
		} else {
			allWorkloads[i].ScanStatus = "pending"
		}

		// Set network policy status based on namespace
		allWorkloads[i].HasNetworkPolicy = nsWithPolicy[allWorkloads[i].Namespace]
	}

	scannedCount := 0
	for _, w := range allWorkloads {
		if w.ScanStatus == "scanned" {
			scannedCount++
		}
	}

	p.logger.Info("workload poll complete", "count", len(allWorkloads), "scanned", scannedCount, "pending", len(allWorkloads)-scannedCount)
	return allWorkloads, nil
}

// getScannedWorkloadSet queries VulnerabilityReports and builds a set of workloads that have been scanned.
// Returns map[namespace/kind/name]bool for efficient lookup.
func (p *Poller) getScannedWorkloadSet(ctx context.Context) map[string]bool {
	scannedSet := make(map[string]bool)

	// Query all VulnerabilityReports
	var namespace string
	if len(p.config.Namespaces) > 0 {
		// Query each configured namespace
		for _, ns := range p.config.Namespaces {
			p.addScannedWorkloadsFromNamespace(ctx, ns, scannedSet)
		}
	} else {
		// Query all namespaces
		p.addScannedWorkloadsFromNamespace(ctx, namespace, scannedSet)
	}

	return scannedSet
}

// addScannedWorkloadsFromNamespace queries VulnerabilityReports in a namespace and adds workload keys to the set.
func (p *Poller) addScannedWorkloadsFromNamespace(ctx context.Context, namespace string, scannedSet map[string]bool) {
	reports, err := p.trivyClient.ListVulnerabilityReports(ctx, namespace)
	if err != nil {
		p.logger.Warn("failed to list vulnerability reports for scanned set", "namespace", namespace, "error", err)
		return
	}

	for _, report := range reports {
		metadata, ok := report["metadata"].(map[string]interface{})
		if !ok {
			continue
		}

		ns, _ := metadata["namespace"].(string)
		labels, _ := metadata["labels"].(map[string]interface{})

		resourceKind, _ := labels["trivy-operator.resource.kind"].(string)
		resourceName, _ := labels["trivy-operator.resource.name"].(string)

		if resourceKind == "" {
			resourceKind = "Pod"
		}

		// Apply the same ReplicaSet->Deployment normalization used in trivy_scanner.go
		resourceKind, resourceName = p.normalizeWorkload(resourceKind, resourceName)

		key := fmt.Sprintf("%s/%s/%s", ns, resourceKind, resourceName)
		scannedSet[key] = true
	}
}

// normalizeWorkload converts ReplicaSet to parent Deployment.
// This mirrors the logic in trivy_scanner.go to ensure consistent workload keys.
func (p *Poller) normalizeWorkload(kind, name string) (string, string) {
	if kind != "ReplicaSet" {
		return kind, name
	}

	// Check if name has a pod-template-hash suffix (8-10 alphanumeric chars)
	// Pattern: deployment-name + "-" + hash (e.g., "cilium-operator-788bd5fdf9")
	if replicaSetHashPattern.MatchString(name) {
		deploymentName := replicaSetHashPattern.ReplaceAllString(name, "")
		return "Deployment", deploymentName
	}

	return kind, name
}

// replicaSetHashPattern matches the pod-template-hash suffix on ReplicaSet names
var replicaSetHashPattern = regexp.MustCompile(`-[a-z0-9]{8,10}$`)

// PollScanFailures fetches failed Trivy scan jobs from the cluster.
// Returns failed jobs with details about why they failed (OOMKilled, etc.)
func (p *Poller) PollScanFailures(ctx context.Context) ([]trivy.ScanJob, error) {
	p.logger.Info("polling scan failures")

	failures, err := p.trivyClient.ListFailedScanJobs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list scan failures: %w", err)
	}

	// Enrich with pod-level failure reasons (OOMKilled, etc.)
	for i := range failures {
		podReason, err := p.trivyClient.GetJobPodFailureReason(ctx, failures[i])
		if err != nil {
			p.logger.Warn("failed to get pod failure reason", "job", failures[i].Name, "error", err)
			continue
		}
		if podReason != "" {
			failures[i].FailureReason = podReason
		}
	}

	p.logger.Info("scan failures poll complete", "count", len(failures))
	return failures, nil
}

// PollClusterResources fetches additional cluster resources for risk analysis.
// This includes ServiceAccounts, Namespaces, and Nodes.
func (p *Poller) PollClusterResources(ctx context.Context) (*ClusterResourcesData, error) {
	p.logger.Info("polling cluster resources")

	k8sClient := p.trivyClient.K8sClient()
	data := &ClusterResourcesData{}

	// Detect cluster info (provider, platform, version)
	clusterInfo, err := k8sClient.DetectClusterInfo(ctx)
	if err != nil {
		p.logger.Warn("failed to detect cluster info", "error", err)
	} else {
		data.ClusterInfo = clusterInfo
		p.logger.Info("detected cluster info",
			"provider", clusterInfo.Provider,
			"platform", clusterInfo.Platform,
			"control_plane", clusterInfo.ControlPlaneType,
			"version", clusterInfo.KubeVersion)
	}

	// Poll ServiceAccounts with RBAC bindings
	if len(p.config.Namespaces) > 0 {
		for _, ns := range p.config.Namespaces {
			sas, err := k8sClient.ListServiceAccounts(ctx, ns)
			if err != nil {
				p.logger.Warn("failed to list service accounts", "namespace", ns, "error", err)
				continue
			}
			data.ServiceAccounts = append(data.ServiceAccounts, sas...)
		}
	} else {
		sas, err := k8sClient.ListServiceAccounts(ctx, "")
		if err != nil {
			p.logger.Warn("failed to list service accounts", "error", err)
		} else {
			data.ServiceAccounts = sas
		}
	}

	// Poll Namespaces (always cluster-scoped)
	namespaces, err := k8sClient.ListNamespaces(ctx)
	if err != nil {
		p.logger.Warn("failed to list namespaces", "error", err)
	} else {
		data.Namespaces = namespaces
	}

	// Poll Nodes (always cluster-scoped)
	nodes, err := k8sClient.ListNodes(ctx)
	if err != nil {
		p.logger.Warn("failed to list nodes", "error", err)
	} else {
		data.Nodes = nodes
	}

	p.logger.Info("cluster resources poll complete",
		"service_accounts", len(data.ServiceAccounts),
		"namespaces", len(data.Namespaces),
		"nodes", len(data.Nodes))

	return data, nil
}

// ClusterResourcesData holds all cluster resource information for risk analysis.
type ClusterResourcesData struct {
	ClusterInfo     *kubectl.ClusterInfo         `json:"cluster_info,omitempty"`
	ServiceAccounts []kubectl.ServiceAccountInfo `json:"service_accounts"`
	Namespaces      []kubectl.NamespaceInfo      `json:"namespaces"`
	Nodes           []kubectl.NodeInfo           `json:"nodes"`
}

// PollExposure analyzes network exposure for all workloads.
// Returns exposure analysis for each workload (Deployments, DaemonSets, StatefulSets).
func (p *Poller) PollExposure(ctx context.Context) ([]kubectl.WorkloadExposure, error) {
	p.logger.Info("polling workload exposure")

	k8sClient := p.trivyClient.K8sClient()
	var allExposures []kubectl.WorkloadExposure

	// Get all workloads first
	var allWorkloads []kubectl.Workload
	if len(p.config.Namespaces) > 0 {
		for _, ns := range p.config.Namespaces {
			workloads, err := k8sClient.ListAllWorkloads(ctx, ns)
			if err != nil {
				p.logger.Warn("failed to list workloads for exposure", "namespace", ns, "error", err)
				continue
			}
			allWorkloads = append(allWorkloads, workloads...)
		}
	} else {
		workloads, err := k8sClient.ListAllWorkloads(ctx, "")
		if err != nil {
			return nil, fmt.Errorf("failed to list workloads for exposure: %w", err)
		}
		allWorkloads = workloads
	}

	// Analyze exposure for each workload
	for _, workload := range allWorkloads {
		// Skip Jobs and CronJobs as they typically don't have services
		if workload.Kind == "Job" || workload.Kind == "CronJob" {
			continue
		}

		exposure, err := k8sClient.AnalyzeWorkloadExposure(ctx, workload)
		if err != nil {
			p.logger.Warn("failed to analyze workload exposure",
				"namespace", workload.Namespace,
				"kind", workload.Kind,
				"name", workload.Name,
				"error", err)
			continue
		}

		allExposures = append(allExposures, *exposure)
	}

	// Log summary
	internetCount := 0
	clusterCount := 0
	namespaceCount := 0
	isolatedCount := 0
	for _, e := range allExposures {
		switch e.ExposureLevel {
		case "internet":
			internetCount++
		case "cluster":
			clusterCount++
		case "namespace":
			namespaceCount++
		case "isolated":
			isolatedCount++
		}
	}

	p.logger.Info("exposure poll complete",
		"total", len(allExposures),
		"internet", internetCount,
		"cluster", clusterCount,
		"namespace", namespaceCount,
		"isolated", isolatedCount)

	return allExposures, nil
}
