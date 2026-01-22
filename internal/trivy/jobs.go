package trivy

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ScanJob represents a Trivy scan job with its status
type ScanJob struct {
	Name            string    `json:"name"`
	Namespace       string    `json:"namespace"`
	TargetKind      string    `json:"target_kind,omitempty"`
	TargetNamespace string    `json:"target_namespace,omitempty"`
	TargetName      string    `json:"target_name,omitempty"`
	Status          string    `json:"status"`         // Running, Succeeded, Failed
	FailureReason   string    `json:"failure_reason"` // OOMKilled, BackoffLimitExceeded, etc.
	FailedAt        time.Time `json:"failed_at"`
}

// ListFailedScanJobs lists failed Trivy scan jobs from the trivy-system namespace.
// These jobs are created by the Trivy Operator to scan workloads.
func (c *Client) ListFailedScanJobs(ctx context.Context) ([]ScanJob, error) {
	// trivy-operator creates jobs in trivy-system namespace
	// with label app.kubernetes.io/managed-by=trivy-operator
	jobs, err := c.clientset.BatchV1().Jobs("trivy-system").List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/managed-by=trivy-operator",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list trivy jobs: %w", err)
	}

	var failedJobs []ScanJob

	for _, job := range jobs.Items {
		// Only include failed jobs
		if !isJobFailed(job) {
			continue
		}

		scanJob := ScanJob{
			Name:      job.Name,
			Namespace: job.Namespace,
			Status:    "Failed",
		}

		// Extract target workload info from labels
		if labels := job.Labels; labels != nil {
			scanJob.TargetKind = labels["trivy-operator.resource.kind"]
			scanJob.TargetNamespace = labels["trivy-operator.resource.namespace"]
			scanJob.TargetName = labels["trivy-operator.resource.name"]
		}

		// Determine failure reason
		scanJob.FailureReason = getJobFailureReason(job)
		scanJob.FailedAt = getJobFailedTime(job)

		failedJobs = append(failedJobs, scanJob)
	}

	return failedJobs, nil
}

// isJobFailed checks if a Job has failed
func isJobFailed(job batchv1.Job) bool {
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// getJobFailureReason determines why a Job failed
func getJobFailureReason(job batchv1.Job) string {
	// Check job conditions for reason
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			if condition.Reason != "" {
				return condition.Reason
			}
		}
	}

	// Fallback: check for common patterns
	if job.Status.Failed > 0 {
		return "BackoffLimitExceeded"
	}

	return "Unknown"
}

// getJobFailedTime returns when the job failed
func getJobFailedTime(job batchv1.Job) time.Time {
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			if !condition.LastTransitionTime.IsZero() {
				return condition.LastTransitionTime.Time
			}
		}
	}
	// Fallback to completion time or creation time
	if job.Status.CompletionTime != nil {
		return job.Status.CompletionTime.Time
	}
	return job.CreationTimestamp.Time
}

// GetJobPodFailureReason gets detailed failure reason from the pod (e.g., OOMKilled)
func (c *Client) GetJobPodFailureReason(ctx context.Context, job ScanJob) (string, error) {
	// List pods for this job
	pods, err := c.clientset.CoreV1().Pods(job.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("job-name=%s", job.Name),
	})
	if err != nil {
		return "", fmt.Errorf("failed to list pods for job: %w", err)
	}

	// Check each pod for container status
	for _, pod := range pods.Items {
		for _, containerStatus := range pod.Status.ContainerStatuses {
			if containerStatus.State.Terminated != nil {
				reason := containerStatus.State.Terminated.Reason
				if reason == "OOMKilled" {
					return "OOMKilled", nil
				}
				if reason != "" && reason != "Completed" {
					return reason, nil
				}
			}
			// Also check last termination state
			if containerStatus.LastTerminationState.Terminated != nil {
				reason := containerStatus.LastTerminationState.Terminated.Reason
				if reason == "OOMKilled" {
					return "OOMKilled", nil
				}
			}
		}
	}

	return "", nil
}
