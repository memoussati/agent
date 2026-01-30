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
	if string(FindingTypeVulnerability) != "vulnerability" {
		t.Errorf("FindingTypeVulnerability = %q, want %q", FindingTypeVulnerability, "vulnerability")
	}
}
