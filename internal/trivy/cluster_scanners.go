package trivy

import (
	"context"
	"fmt"
)

// ClusterVulnScanner scans cluster-scoped vulnerability reports
type ClusterVulnScanner struct {
	client *Client
}

func NewClusterVulnScanner(client *Client) *ClusterVulnScanner {
	return &ClusterVulnScanner{client: client}
}

func (s *ClusterVulnScanner) Name() string {
	return "cluster-vulns"
}

func (s *ClusterVulnScanner) Scan(ctx context.Context, _ string) ([]Finding, error) {
	reports, err := s.client.ListClusterVulnerabilityReports(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster vulnerability reports: %w", err)
	}

	var findings []Finding
	for _, report := range reports {
		metadata, ok := report["metadata"].(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := metadata["name"].(string)

		// Extract artifact info for cluster-scoped reports
		artifact := extractClusterArtifactInfo(report)

		vulns, err := s.client.ParseVulnerabilities(report)
		if err != nil {
			continue
		}

		for _, v := range vulns {
			finding := VulnerabilityToFinding(v, "", "Cluster", name, artifact)
			findings = append(findings, finding)
		}
	}
	return findings, nil
}

// extractClusterArtifactInfo extracts artifact info from cluster vulnerability reports
func extractClusterArtifactInfo(report map[string]interface{}) ArtifactInfo {
	artifact := ArtifactInfo{}

	// Navigate to report.artifact
	reportData, ok := report["report"].(map[string]interface{})
	if !ok {
		return artifact
	}

	artifactData, ok := reportData["artifact"].(map[string]interface{})
	if !ok {
		return artifact
	}

	artifact.Repository, _ = artifactData["repository"].(string)
	artifact.Tag, _ = artifactData["tag"].(string)
	artifact.Digest, _ = artifactData["digest"].(string)

	return artifact
}
