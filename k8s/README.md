# Lang-Ango K8s Sidecar Injector

## Deploy

```bash
# Build and push injector image
docker build -t langango/injector:latest ./k8s/injector
docker push langango/injector:latest

# Deploy webhook
kubectl apply -f k8s/injector/deployment.yaml
kubectl apply -f k8s/injector/service.yaml
kubectl apply -f k8s/injector/rbac.yaml
kubectl apply -f k8s/injector/webhook.yaml
```

## Usage

Add annotation to pod:

```yaml
apiVersion: v1
kind: Pod
metadata:
  annotations:
    langango-enabled: "true"
spec:
  containers:
  - name: app
    image: myapp:latest
```

Or add label to deployment:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  labels:
    langango-enabled: "true"
```
