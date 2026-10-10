# Brainbird Kubernetes Build and Deploy Script
Write-Host "=== Brainbird Kubernetes Deployment ===" -ForegroundColor Cyan

# 1. Check Docker Desktop / Daemon
Write-Host "1. Checking Docker..." -ForegroundColor Yellow
$dockerOk = docker info 2>$null
if (-not $dockerOk) {
    Write-Host "ERROR: Docker is not running. Please start Docker Desktop and ensure Kubernetes is enabled in Settings -> Kubernetes." -ForegroundColor Red
    exit 1
}

# 2. Check kubectl connection
Write-Host "2. Checking Kubernetes cluster..." -ForegroundColor Yellow
$clusterOk = kubectl cluster-info 2>$null
if (-not $clusterOk) {
    Write-Host "ERROR: Cannot connect to Kubernetes cluster. Please enable Kubernetes in Docker Desktop Settings." -ForegroundColor Red
    exit 1
}

# 3. Build Docker image
Write-Host "3. Building Docker image: brainbird-backend:latest..." -ForegroundColor Yellow
docker build -t brainbird-backend:latest -f ../backend/Dockerfile ..
if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: Docker build failed." -ForegroundColor Red
    exit 1
}

# 4. Apply Kubernetes Manifests
Write-Host "4. Applying Kubernetes manifests..." -ForegroundColor Yellow
kubectl apply -k .
if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: kubectl apply failed." -ForegroundColor Red
    exit 1
}

# 5. Wait for deployment rollout
Write-Host "5. Waiting for rollout..." -ForegroundColor Yellow
kubectl rollout status deployment/brainbird-backend -n brainbird --timeout=60s

Write-Host "`n=== Deployment Successful! ===" -ForegroundColor Green
Write-Host "Access URL: http://localhost:8081 (with port-forward) or http://localhost:30081 (NodePort)" -ForegroundColor Cyan
Write-Host "To port-forward: kubectl port-forward -n brainbird svc/brainbird-backend-service 8081:8081"
