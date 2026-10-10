# Kubernetes deployment

The Kubernetes Secret is intentionally not stored in this repository. Apply the namespace, create the Secret with deployment-specific credentials, then apply the remaining manifests:

```powershell
kubectl apply -f namespace.yaml
kubectl -n brainbird create secret generic brainbird-secrets `
  --from-literal=DATABASE_URL="<set-the-database-url>" `
  --from-literal=JWT_SECRET="<set-a-strong-random-secret>" `
  --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -k .
```

Use a strong, unique JWT secret and a database account with only the required permissions. Do not commit the generated Secret or credentials.
