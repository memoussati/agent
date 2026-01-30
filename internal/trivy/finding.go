package trivy

import "fmt"

type Severity string

const (
	SeverityCritical Severity = "CRITICAL"
	SeverityHigh     Severity = "HIGH"
	SeverityMedium   Severity = "MEDIUM"
	SeverityLow      Severity = "LOW"
	SeverityUnknown  Severity = "UNKNOWN"
)

type FindingType string

const (
	FindingTypeVulnerability FindingType = "vulnerability"
)

type Finding struct {
	// Identity
	ID   string      `json:"id"`
	Type FindingType `json:"type"`

	// Severity
	Severity Severity `json:"severity"`
	Score    float64  `json:"score,omitempty"` //CVSS score if available

	// Location - where in the cluster
	Namespace    string `json:"namespace,omitempty"`
	ResourceKind string `json:"resourceKind,omitempty"`
	ResourceName string `json:"resourceName,omitempty"`

	// Container/Image - for per-container tracking
	ContainerName   string `json:"containerName,omitempty"`
	ImageRepository string `json:"imageRepository,omitempty"`
	ImageTag        string `json:"imageTag,omitempty"`
	ImageDigest     string `json:"imageDigest,omitempty"`

	// Description
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Remediation string `json:"remediation,omitempty"`

	// Metadata
	Source    string `json:"source"`
	CreatedAt string `json:"createdAt,omitempty"`

	// Raw data from the source (for detailed inspection)
	RawData interface{} `json:"rawData,omitempty"`
}

// ArtifactInfo contains container image details from Trivy reports.
type ArtifactInfo struct {
	ContainerName string
	Repository    string
	Tag           string
	Digest        string
}

// VulnerabilityToFinding converts a Trivy vulnerability to a Finding
func VulnerabilityToFinding(v Vulnerability, namespace, resourceKind, resourceName string, artifact ArtifactInfo) Finding {
	return Finding{
		ID:              v.VulnerabilityID,
		Type:            FindingTypeVulnerability,
		Severity:        Severity(v.Severity), // Convert string to Severity type
		Score:           v.Score,
		Namespace:       namespace,
		ResourceKind:    resourceKind,
		ResourceName:    resourceName,
		ContainerName:   artifact.ContainerName,
		ImageRepository: artifact.Repository,
		ImageTag:        artifact.Tag,
		ImageDigest:     artifact.Digest,
		Title:           v.Title,
		Description:     fmt.Sprintf("%s %s (installed: %s, fixed: %s)", v.PkgName, v.VulnerabilityID, v.InstalledVersion, v.FixedVersion),
		Remediation:     fmt.Sprintf("Update %s to version %s", v.PkgName, v.FixedVersion),
		Source:          "trivy",
		RawData:         v,
	}
}
