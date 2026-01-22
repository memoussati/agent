<div align="center">
  <h1>trix-agent</h1>
  <p><strong>Continuous Security Monitoring Agent for Kubernetes</strong></p>

  <p>
    Long-running service that monitors your cluster's security posture<br>
    and sends notifications when vulnerabilities are discovered or fixed.
  </p>

  <p>
    <a href="https://go.dev/"><img src="https://img.shields.io/github/go-mod/go-version/trixsec-dev/trix-agent" alt="Go Version"></a>
    <a href="LICENSE"><img src="https://img.shields.io/github/license/trixsec-dev/trix-agent" alt="License"></a>
    <a href="https://goreportcard.com/report/github.com/trixsec-dev/trix-agent"><img src="https://goreportcard.com/badge/github.com/trixsec-dev/trix-agent" alt="Go Report Card"></a>
    <a href="https://github.com/trixsec-dev/trix-agent/releases"><img src="https://img.shields.io/github/v/release/trixsec-dev/trix-agent?include_prereleases" alt="Release"></a>
  </p>

  <p>
    <a href="#features"><strong>Features</strong></a> |
    <a href="#installation"><strong>Installation</strong></a> |
    <a href="#configuration"><strong>Configuration</strong></a> |
    <a href="#deployment"><strong>Deployment</strong></a> |
    <a href="#notifications"><strong>Notifications</strong></a>
  </p>
</div>

---

## Features

- **Continuous Monitoring** - Polls Trivy Operator CRDs at configurable intervals
- **Vulnerability Lifecycle Tracking** - Tracks new and fixed vulnerabilities in PostgreSQL
- **Multi-Channel Notifications** - Slack, generic webhooks, and Trix SaaS integration
- **Health Endpoints** - Kubernetes-ready health checks for monitoring
- **Namespace Filtering** - Monitor specific namespaces or entire cluster
- **Configurable Severity** - Choose which severity levels trigger notifications
- **Structured Logging** - JSON or text logging with configurable levels

## How it Works

trix-agent runs as a service in your Kubernetes cluster and continuously:

1. **Polls** Trivy Operator CRDs for security findings
2. **Tracks** vulnerability state changes in PostgreSQL database
3. **Detects** new vulnerabilities and when vulnerabilities are fixed
4. **Notifies** through configured channels when changes occur

The agent maintains a history of findings, allowing it to distinguish between:
- **New vulnerabilities** - Never seen before
- **Fixed vulnerabilities** - Previously detected but now resolved
- **Persistent vulnerabilities** - Ongoing issues that need attention

## Installation

### Prerequisites

- Kubernetes cluster with [Trivy Operator](https://aquasecurity.github.io/trivy-operator/) installed
- PostgreSQL database (for vulnerability state tracking)

### Quick Install with Helm

```bash
# Add the Helm repository
helm repo add trix https://trixsec-dev.github.io/trix
helm repo update

# Install the agent
helm install trix-agent trix/trix-agent \
  --namespace trix-system \
  --create-namespace \
  --set databaseUrl=postgresql://user:pass@host:5432/dbname
```

### Manual Installation

**Create namespace and secrets:**

```bash
kubectl create namespace trix-system

# Create database secret
kubectl create secret generic trix-agent-db \
  --namespace trix-system \
  --from-literal=database-url="postgresql://user:pass@host:5432/dbname"

# Optional: Slack webhook secret
kubectl create secret generic trix-agent-notifications \
  --namespace trix-system \
  --from-literal=slack-webhook="https://hooks.slack.com/services/YOUR/SLACK/WEBHOOK"
```

**Deploy the agent:**

```bash
kubectl apply -f https://raw.githubusercontent.com/trixsec-dev/trix-agent/main/deploy/kubernetes/deployment.yaml
```

### Build from Source

```bash
git clone https://github.com/trixsec-dev/trix-agent.git
cd trix-agent
go build -o trix-agent ./cmd/...
```

## Configuration

trix-agent is configured via environment variables:

### Database

| Variable | Description | Required |
|----------|-------------|----------|
| `TRIX_DATABASE_URL` | PostgreSQL connection string | ✅ |

### Polling & Filtering

| Variable | Description | Default |
|----------|-------------|---------|
| `TRIX_POLL_INTERVAL` | How often to poll Trivy Operator | `5m` |
| `TRIX_NAMESPACES` | Namespaces to watch (comma-separated) | all |
| `TRIX_CLUSTER_NAME` | Human-readable cluster name | hostname |

### Notifications

| Variable | Description | Default |
|----------|-------------|---------|
| `TRIX_NOTIFY_SLACK` | Slack incoming webhook URL | - |
| `TRIX_NOTIFY_WEBHOOK` | Generic webhook URL | - |
| `TRIX_NOTIFY_SEVERITY` | Minimum severity to notify | `CRITICAL` |

### SaaS Integration

| Variable | Description | Default |
|----------|-------------|---------|
| `TRIX_SAAS_ENDPOINT` | Trix SaaS API endpoint | - |
| `TRIX_SAAS_API_KEY` | API key for SaaS authentication | - |

### Logging & Health

| Variable | Description | Default |
|----------|-------------|---------|
| `TRIX_LOG_FORMAT` | Log format (`json` or `text`) | `json` |
| `TRIX_LOG_LEVEL` | Log level (`debug`, `info`, `warn`, `error`) | `info` |
| `TRIX_HEALTH_ADDR` | Health check server address | `:8080` |

## Deployment

### Kubernetes Deployment

The recommended way to deploy trix-agent is using the provided Kubernetes manifests:

```bash
# Deploy with default configuration
kubectl apply -f deploy/kubernetes/

# Deploy with custom values
kubectl apply -f deploy/kubernetes/patches/
```

**Key deployment components:**

- **Deployment** - Runs the agent with replicas for high availability
- **Service** - Exposes health endpoints for Kubernetes probes
- **ServiceAccount** - Minimal RBAC permissions for Trivy Operator access
- **ConfigMap** - Default configuration
- **Secrets** - Database URL and notification webhooks

### Helm Chart

For more advanced deployments, use the Helm chart:

```bash
# Install with custom values
helm install trix-agent trix/trix-agent \
  --namespace trix-system \
  --create-namespace \
  --values values.yaml

# Example values.yaml
databaseUrl: "postgresql://user:pass@postgres:5432/trix"
clusterName: "production-eu"
notifications:
  slack:
    webhookUrl: "https://hooks.slack.com/services/..."
  severity: "HIGH"
resources:
  limits:
    memory: "256Mi"
    cpu: "200m"
  requests:
    memory: "128Mi"
    cpu: "100m"
```

## Notifications

### Slack Notifications

Configure Slack integration to receive notifications about security changes:

```bash
# Create Slack incoming webhook
export TRIX_NOTIFY_SLACK="https://hooks.slack.com/services/YOUR/SLACK/WEBHOOK"
```

**Slack message format:**
```
🚨 New Critical Vulnerabilities in production

Cluster: production-eu
Namespace: default

Workloads affected:
• web-app: CVE-2024-45337 (golang.org/x/crypto)
• api-server: CVE-2024-3817 (nginx)

View details: https://trix.example.com/clusters/production-eu
```

### Generic Webhooks

Send notifications to any webhook endpoint:

```bash
export TRIX_NOTIFY_WEBHOOK="https://your-webhook.example.com/security"
```

**Webhook payload:**
```json
{
  "cluster": "production-eu",
  "event": "vulnerabilities_fixed",
  "severity": "CRITICAL",
  "count": 3,
  "workloads": ["web-app", "api-server"],
  "timestamp": "2024-01-15T10:30:00Z"
}
```

### Trix SaaS Integration

Connect to the Trix SaaS platform for centralized vulnerability management:

```bash
export TRIX_SAAS_ENDPOINT="https://api.trix.example.com"
export TRIX_SAAS_API_KEY="your-api-key-here"
```

## Health Checks

trix-agent provides HTTP endpoints for Kubernetes health probes:

- **`GET /health/live`** - Liveness probe (always returns 200 if service is running)
- **`GET /health/ready`** - Readiness probe (checks database connectivity)
- **`GET /metrics`** - Prometheus metrics (if enabled)

**Example probe configuration:**
```yaml
livenessProbe:
  httpGet:
    path: /health/live
    port: 8080
  initialDelaySeconds: 30
  periodSeconds: 10

readinessProbe:
  httpGet:
    path: /health/ready
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 5
```

## Monitoring and Observability

### Logs

trix-agent uses structured logging with contextual information:

```json
{
  "level": "info",
  "msg": "vulnerabilities detected",
  "cluster": "production-eu",
  "namespace": "default",
  "new_count": 3,
  "fixed_count": 1,
  "timestamp": "2024-01-15T10:30:00Z"
}
```

### Database Schema

The agent creates the necessary tables in PostgreSQL:

```sql
-- Track vulnerability lifecycle
CREATE TABLE vulnerability_history (
  id SERIAL PRIMARY KEY,
  cluster_name VARCHAR(255),
  namespace VARCHAR(255),
  workload VARCHAR(255),
  cve_id VARCHAR(50),
  severity VARCHAR(20),
  first_seen TIMESTAMP,
  last_seen TIMESTAMP,
  fixed_at TIMESTAMP
);
```

## Security Considerations

- **Minimal RBAC** - Agent requires only read access to Trivy Operator resources
- **Non-root Container** - Runs as user `65534:65534` in scratch image
- **Secret Management** - All sensitive data stored in Kubernetes secrets
- **Network Policies** - Can be restricted to database and outbound webhook access only

## Troubleshooting

### Common Issues

**Agent not finding vulnerabilities:**
```bash
# Check Trivy Operator is running
kubectl get pods -n trivy-system

# Verify agent can access CRDs
kubectl get vulnerabilityreports --all-namespaces
```

**Database connection errors:**
```bash
# Check database connectivity
kubectl exec -n trix-system deployment/trix-agent -- \
  /trix-agent --database-url=$TRIX_DATABASE_URL --dry-run
```

**Notification failures:**
```bash
# Test webhook manually
curl -X POST "$TRIX_NOTIFY_WEBHOOK" \
  -H "Content-Type: application/json" \
  -d '{"test": true}'
```

### Debug Mode

Enable debug logging for detailed troubleshooting:

```bash
# Update ConfigMap or set environment variable
TRIX_LOG_LEVEL=debug
TRIX_LOG_FORMAT=text
```

## Development

### Local Development

```bash
# Install dependencies
go mod download

# Run tests
go test -v ./...

# Build
go build -o trix-agent ./cmd/...

# Run locally (requires kubeconfig)
export TRIX_DATABASE_URL="postgres://..."
./trix-agent
```

### Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests for new functionality
5. Run `go test ./...` and `go fmt ./...`
6. Submit a pull request

## License

Distributed under the Apache 2.0 License. See [LICENSE](LICENSE) for more information.
