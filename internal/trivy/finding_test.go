package trivy

import (
	"strings"
	"testing"
)

func TestVulnerabilityToFinding(t *testing.T) {
	tests := []struct {
		name         string
		vuln         Vulnerability
		namespace    string
		resourceKind string
		resourceName string
		artifact     ArtifactInfo
		wantID       string
		wantType     FindingType
		wantSeverity Severity
	}{
		{
			name: "full vulnerability with all fields",
			vuln: Vulnerability{
				VulnerabilityID:  "CVE-2024-1234",
				PkgName:          "openssl",
				InstalledVersion: "1.1.1",
				FixedVersion:     "1.1.2",
				Severity:         "CRITICAL",
				Score:            9.8,
				Title:            "OpenSSL Buffer Overflow",
			},
			namespace:    "production",
			resourceKind: "Deployment",
			resourceName: "web-app",
			artifact: ArtifactInfo{
				ContainerName: "nginx",
				Repository:    "docker.io/library/nginx",
				Tag:           "1.25",
				Digest:        "sha256:abc123",
			},
			wantID:       "CVE-2024-1234",
			wantType:     FindingTypeVulnerability,
			wantSeverity: SeverityCritical,
		},
		{
			name: "vulnerability with empty optional fields",
			vuln: Vulnerability{
				VulnerabilityID: "CVE-2024-5678",
				PkgName:         "curl",
				Severity:        "LOW",
			},
			namespace:    "",
			resourceKind: "",
			resourceName: "",
			artifact:     ArtifactInfo{},
			wantID:       "CVE-2024-5678",
			wantType:     FindingTypeVulnerability,
			wantSeverity: SeverityLow,
		},
		{
			name: "vulnerability with unknown severity",
			vuln: Vulnerability{
				VulnerabilityID: "GHSA-1234",
				Severity:        "UNKNOWN",
			},
			namespace:    "default",
			resourceKind: "Pod",
			resourceName: "test-pod",
			artifact:     ArtifactInfo{},
			wantID:       "GHSA-1234",
			wantType:     FindingTypeVulnerability,
			wantSeverity: SeverityUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := VulnerabilityToFinding(tt.vuln, tt.namespace, tt.resourceKind, tt.resourceName, tt.artifact)

			if got.ID != tt.wantID {
				t.Errorf("ID = %q, want %q", got.ID, tt.wantID)
			}
			if got.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", got.Type, tt.wantType)
			}
			if got.Severity != tt.wantSeverity {
				t.Errorf("Severity = %q, want %q", got.Severity, tt.wantSeverity)
			}
			if got.Source != "trivy" {
				t.Errorf("Source = %q, want %q", got.Source, "trivy")
			}
			if got.Namespace != tt.namespace {
				t.Errorf("Namespace = %q, want %q", got.Namespace, tt.namespace)
			}
			if got.ResourceKind != tt.resourceKind {
				t.Errorf("ResourceKind = %q, want %q", got.ResourceKind, tt.resourceKind)
			}
			if got.ResourceName != tt.resourceName {
				t.Errorf("ResourceName = %q, want %q", got.ResourceName, tt.resourceName)
			}
			if got.ContainerName != tt.artifact.ContainerName {
				t.Errorf("ContainerName = %q, want %q", got.ContainerName, tt.artifact.ContainerName)
			}
			if got.ImageRepository != tt.artifact.Repository {
				t.Errorf("ImageRepository = %q, want %q", got.ImageRepository, tt.artifact.Repository)
			}
			if got.ImageTag != tt.artifact.Tag {
				t.Errorf("ImageTag = %q, want %q", got.ImageTag, tt.artifact.Tag)
			}
			if got.ImageDigest != tt.artifact.Digest {
				t.Errorf("ImageDigest = %q, want %q", got.ImageDigest, tt.artifact.Digest)
			}
			if got.Score != tt.vuln.Score {
				t.Errorf("Score = %v, want %v", got.Score, tt.vuln.Score)
			}
			if got.Title != tt.vuln.Title {
				t.Errorf("Title = %q, want %q", got.Title, tt.vuln.Title)
			}
			if got.RawData == nil {
				t.Error("RawData should not be nil")
			}
		})
	}
}

func TestVulnerabilityToFinding_Description(t *testing.T) {
	vuln := Vulnerability{
		VulnerabilityID:  "CVE-2024-1234",
		PkgName:          "openssl",
		InstalledVersion: "1.1.1",
		FixedVersion:     "1.1.2",
	}

	got := VulnerabilityToFinding(vuln, "", "", "", ArtifactInfo{})

	// Description should contain package info
	if !strings.Contains(got.Description, "openssl") {
		t.Errorf("Description should contain package name, got %q", got.Description)
	}
	if !strings.Contains(got.Description, "CVE-2024-1234") {
		t.Errorf("Description should contain CVE ID, got %q", got.Description)
	}
	if !strings.Contains(got.Description, "1.1.1") {
		t.Errorf("Description should contain installed version, got %q", got.Description)
	}
	if !strings.Contains(got.Description, "1.1.2") {
		t.Errorf("Description should contain fixed version, got %q", got.Description)
	}
}

func TestVulnerabilityToFinding_Remediation(t *testing.T) {
	vuln := Vulnerability{
		PkgName:      "curl",
		FixedVersion: "8.0.0",
	}

	got := VulnerabilityToFinding(vuln, "", "", "", ArtifactInfo{})

	if !strings.Contains(got.Remediation, "curl") {
		t.Errorf("Remediation should contain package name, got %q", got.Remediation)
	}
	if !strings.Contains(got.Remediation, "8.0.0") {
		t.Errorf("Remediation should contain fixed version, got %q", got.Remediation)
	}
}

func TestComplianceCheckToFinding(t *testing.T) {
	tests := []struct {
		name         string
		check        ComplianceCheck
		namespace    string
		resourceName string
		wantType     FindingType
		wantKind     string
	}{
		{
			name: "full compliance check",
			check: ComplianceCheck{
				CheckID:     "KSV001",
				Title:       "Process can elevate its own privileges",
				Description: "Container should not allow privilege escalation",
				Severity:    "HIGH",
				Category:    "general",
				Success:     false,
				Remediation: "Set allowPrivilegeEscalation to false",
			},
			namespace:    "default",
			resourceName: "web-pod",
			wantType:     FindingTypeCompliance,
			wantKind:     "Pod",
		},
		{
			name: "compliance check with empty fields",
			check: ComplianceCheck{
				CheckID:  "KSV002",
				Severity: "MEDIUM",
			},
			namespace:    "",
			resourceName: "",
			wantType:     FindingTypeCompliance,
			wantKind:     "Pod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComplianceCheckToFinding(tt.check, tt.namespace, tt.resourceName)

			if got.ID != tt.check.CheckID {
				t.Errorf("ID = %q, want %q", got.ID, tt.check.CheckID)
			}
			if got.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", got.Type, tt.wantType)
			}
			if got.ResourceKind != tt.wantKind {
				t.Errorf("ResourceKind = %q, want %q", got.ResourceKind, tt.wantKind)
			}
			if got.Severity != Severity(tt.check.Severity) {
				t.Errorf("Severity = %q, want %q", got.Severity, tt.check.Severity)
			}
			if got.Namespace != tt.namespace {
				t.Errorf("Namespace = %q, want %q", got.Namespace, tt.namespace)
			}
			if got.ResourceName != tt.resourceName {
				t.Errorf("ResourceName = %q, want %q", got.ResourceName, tt.resourceName)
			}
			if got.Title != tt.check.Title {
				t.Errorf("Title = %q, want %q", got.Title, tt.check.Title)
			}
			if got.Description != tt.check.Description {
				t.Errorf("Description = %q, want %q", got.Description, tt.check.Description)
			}
			if got.Remediation != tt.check.Remediation {
				t.Errorf("Remediation = %q, want %q", got.Remediation, tt.check.Remediation)
			}
			if got.Source != "trivy" {
				t.Errorf("Source = %q, want %q", got.Source, "trivy")
			}
		})
	}
}

func TestInfraCheckToFinding(t *testing.T) {
	check := ComplianceCheck{
		CheckID:     "AVD-KSV-0001",
		Title:       "Privileged container",
		Description: "Container runs in privileged mode",
		Severity:    "CRITICAL",
		Remediation: "Remove privileged: true",
	}

	got := InfraCheckToFinding(check, "kube-system", "coredns")

	if got.Type != FindingTypeInfra {
		t.Errorf("Type = %q, want %q", got.Type, FindingTypeInfra)
	}
	if got.ResourceKind != "Pod" {
		t.Errorf("ResourceKind = %q, want %q", got.ResourceKind, "Pod")
	}
	if got.ID != check.CheckID {
		t.Errorf("ID = %q, want %q", got.ID, check.CheckID)
	}
	if got.Namespace != "kube-system" {
		t.Errorf("Namespace = %q, want %q", got.Namespace, "kube-system")
	}
	if got.Source != "trivy" {
		t.Errorf("Source = %q, want %q", got.Source, "trivy")
	}
}

func TestRbacCheckToFinding(t *testing.T) {
	check := ComplianceCheck{
		CheckID:     "KSV-RBAC-001",
		Title:       "Cluster admin binding",
		Description: "Role binding grants cluster-admin",
		Severity:    "HIGH",
		Remediation: "Use least privilege principle",
	}

	got := RbacCheckToFinding(check, "default", "admin-binding")

	if got.Type != FindingTypeRBAC {
		t.Errorf("Type = %q, want %q", got.Type, FindingTypeRBAC)
	}
	if got.ResourceKind != "Role" {
		t.Errorf("ResourceKind = %q, want %q", got.ResourceKind, "Role")
	}
	if got.ID != check.CheckID {
		t.Errorf("ID = %q, want %q", got.ID, check.CheckID)
	}
	if got.Namespace != "default" {
		t.Errorf("Namespace = %q, want %q", got.Namespace, "default")
	}
	if got.ResourceName != "admin-binding" {
		t.Errorf("ResourceName = %q, want %q", got.ResourceName, "admin-binding")
	}
}

func TestExposedSecretToFinding(t *testing.T) {
	tests := []struct {
		name         string
		secret       ExposedSecret
		namespace    string
		resourceName string
	}{
		{
			name: "AWS secret key",
			secret: ExposedSecret{
				Target:   "/app/config.yaml",
				RuleID:   "aws-access-key-id",
				Title:    "AWS Access Key ID",
				Category: "AWS",
				Severity: "CRITICAL",
				Match:    "AKIA***",
			},
			namespace:    "production",
			resourceName: "api-server",
		},
		{
			name: "generic secret with empty target",
			secret: ExposedSecret{
				RuleID:   "generic-api-key",
				Severity: "HIGH",
			},
			namespace:    "",
			resourceName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExposedSecretToFinding(tt.secret, tt.namespace, tt.resourceName)

			if got.Type != FindingTypeSecret {
				t.Errorf("Type = %q, want %q", got.Type, FindingTypeSecret)
			}
			if got.ID != tt.secret.RuleID {
				t.Errorf("ID = %q, want %q", got.ID, tt.secret.RuleID)
			}
			if got.Severity != Severity(tt.secret.Severity) {
				t.Errorf("Severity = %q, want %q", got.Severity, tt.secret.Severity)
			}
			if got.ResourceKind != "Pod" {
				t.Errorf("ResourceKind = %q, want %q", got.ResourceKind, "Pod")
			}
			if got.Title != tt.secret.Title {
				t.Errorf("Title = %q, want %q", got.Title, tt.secret.Title)
			}
			if got.Source != "trivy" {
				t.Errorf("Source = %q, want %q", got.Source, "trivy")
			}
			// Description should mention the target
			if tt.secret.Target != "" && !strings.Contains(got.Description, tt.secret.Target) {
				t.Errorf("Description should contain target %q, got %q", tt.secret.Target, got.Description)
			}
		})
	}
}

func TestBenchmarkControlToFinding(t *testing.T) {
	tests := []struct {
		name          string
		control       BenchmarkControl
		benchmarkName string
	}{
		{
			name: "CIS benchmark control with failures",
			control: BenchmarkControl{
				ID:        "1.2.3",
				Name:      "Ensure API server audit logging is enabled",
				Severity:  "MEDIUM",
				TotalFail: 5,
			},
			benchmarkName: "cis-kubernetes-1.8",
		},
		{
			name: "NSA benchmark control with no failures",
			control: BenchmarkControl{
				ID:        "NSA-001",
				Name:      "Network policies defined",
				Severity:  "LOW",
				TotalFail: 0,
			},
			benchmarkName: "nsa-kubernetes-hardening",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BenchmarkControlToFinding(tt.control, tt.benchmarkName)

			if got.Type != FindingTypeBenchmark {
				t.Errorf("Type = %q, want %q", got.Type, FindingTypeBenchmark)
			}
			if got.ID != tt.control.ID {
				t.Errorf("ID = %q, want %q", got.ID, tt.control.ID)
			}
			if got.Severity != Severity(tt.control.Severity) {
				t.Errorf("Severity = %q, want %q", got.Severity, tt.control.Severity)
			}
			if got.ResourceKind != "Cluster" {
				t.Errorf("ResourceKind = %q, want %q", got.ResourceKind, "Cluster")
			}
			if got.ResourceName != tt.benchmarkName {
				t.Errorf("ResourceName = %q, want %q", got.ResourceName, tt.benchmarkName)
			}
			if got.Title != tt.control.Name {
				t.Errorf("Title = %q, want %q", got.Title, tt.control.Name)
			}
			if got.Source != "trivy" {
				t.Errorf("Source = %q, want %q", got.Source, "trivy")
			}
			// Description should contain the control ID
			if !strings.Contains(got.Description, tt.control.ID) {
				t.Errorf("Description should contain control ID %q, got %q", tt.control.ID, got.Description)
			}
		})
	}
}

func TestSeverityConstants(t *testing.T) {
	tests := []struct {
		severity Severity
		want     string
	}{
		{SeverityCritical, "CRITICAL"},
		{SeverityHigh, "HIGH"},
		{SeverityMedium, "MEDIUM"},
		{SeverityLow, "LOW"},
		{SeverityUnknown, "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if string(tt.severity) != tt.want {
				t.Errorf("Severity constant = %q, want %q", tt.severity, tt.want)
			}
		})
	}
}

func TestFindingTypeConstants(t *testing.T) {
	tests := []struct {
		findingType FindingType
		want        string
	}{
		{FindingTypeVulnerability, "vulnerability"},
		{FindingTypeCompliance, "compliance"},
		{FindingTypeRBAC, "rbac"},
		{FindingTypeSecret, "secret"},
		{FindingTypeInfra, "infra"},
		{FindingTypeBenchmark, "benchmark"},
		{FindingTypeEvent, "event"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if string(tt.findingType) != tt.want {
				t.Errorf("FindingType constant = %q, want %q", tt.findingType, tt.want)
			}
		})
	}
}
