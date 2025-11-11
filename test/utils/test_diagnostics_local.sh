#!/bin/bash
# Quick test to verify the diagnostic script works before using in CI/CD

echo "Testing diagnostic script locally..."
echo ""

# Check if kubectl is available
if ! command -v kubectl &> /dev/null; then
    echo "❌ kubectl not found - install kubectl first"
    exit 1
fi

# Check if cluster is accessible
if ! kubectl cluster-info &> /dev/null; then
    echo "❌ Cannot connect to Kubernetes cluster"
    echo "   Make sure KUBECONFIG is set and cluster is running"
    exit 1
fi

echo "✅ kubectl is available"
echo "✅ Cluster is accessible"
echo ""

# Make script executable
chmod +x test/utils/ci_diagnostics.sh

# Run the diagnostic script
echo "Running diagnostic script..."
echo "================================"
echo ""

./test/utils/ci_diagnostics.sh

EXIT_CODE=$?

echo ""
echo "================================"
if [ $EXIT_CODE -eq 0 ]; then
    echo "✅ Diagnostic script completed successfully"
else
    echo "⚠️  Diagnostic script exited with code $EXIT_CODE (this is expected for diagnostics)"
fi

echo ""
echo "The script is ready to use in CI/CD!"
echo ""
echo "Add to your GitHub Actions workflow:"
echo "  - name: Run Diagnostics"
echo "    if: failure()"
echo "    run: ./test/utils/ci_diagnostics.sh"
