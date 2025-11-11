#!/bin/bash
# CI/CD Diagnostic Script for E2E Test Failures
# This script helps diagnose why the controller isn't processing VariantAutoscaling resources

# Don't exit on errors - we want to collect all diagnostics
set +e

NAMESPACE_LLMD="llm-d-sim"
NAMESPACE_CONTROLLER="workload-variant-autoscaler-system"
NAMESPACE_MONITORING="workload-variant-autoscaler-monitoring"

echo "=========================================="
echo "E2E Test Diagnostics for VariantAutoscaling"
echo "=========================================="
echo ""

# Color codes for output (GitHub Actions supports ANSI colors)
if [ -t 1 ] || [ "$GITHUB_ACTIONS" = "true" ]; then
    RED='\033[0;31m'
    GREEN='\033[0;32m'
    YELLOW='\033[1;33m'
    NC='\033[0m' # No Color
else
    RED=''
    GREEN=''
    YELLOW=''
    NC=''
fi

print_header() {
    echo ""
    echo "=========================================="
    echo "$1"
    echo "=========================================="
}

print_success() {
    echo -e "${GREEN}✓ $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠ $1${NC}"
}

print_error() {
    echo -e "${RED}✗ $1${NC}"
}

# 1. Check if namespaces exist
print_header "1. Checking Namespaces"
for ns in $NAMESPACE_LLMD $NAMESPACE_CONTROLLER $NAMESPACE_MONITORING; do
    if kubectl get namespace "$ns" &>/dev/null; then
        print_success "Namespace $ns exists"
    else
        print_error "Namespace $ns DOES NOT exist"
    fi
done

# 2. Check llm-d-sim pods
print_header "2. Checking llm-d-sim Pods"
if kubectl get pods -n $NAMESPACE_LLMD &>/dev/null; then
    POD_COUNT=$(kubectl get pods -n $NAMESPACE_LLMD --no-headers 2>/dev/null | wc -l)
    if [ "$POD_COUNT" -eq 0 ]; then
        print_warning "No pods found in namespace $NAMESPACE_LLMD"
    else
        print_success "Found $POD_COUNT pod(s) in namespace $NAMESPACE_LLMD"
        echo ""
        kubectl get pods -n $NAMESPACE_LLMD -o wide

        # Check pod status
        echo ""
        RUNNING_PODS=$(kubectl get pods -n $NAMESPACE_LLMD --field-selector=status.phase=Running --no-headers 2>/dev/null | wc -l)
        if [ "$RUNNING_PODS" -eq 0 ]; then
            print_error "No pods in Running state!"
            echo ""
            echo "Pod statuses:"
            kubectl get pods -n $NAMESPACE_LLMD -o custom-columns=NAME:.metadata.name,STATUS:.status.phase,REASON:.status.reason
        else
            print_success "$RUNNING_PODS pod(s) in Running state"
        fi
    fi
else
    print_error "Cannot access namespace $NAMESPACE_LLMD"
fi

# 3. Check if metrics endpoint is accessible from one pod
print_header "3. Checking Metrics Endpoint on llm-d-sim Pods"
POD_NAME=$(kubectl get pods -n $NAMESPACE_LLMD --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
if [ -n "$POD_NAME" ]; then
    print_success "Testing metrics endpoint on pod: $POD_NAME"

    # Try to curl metrics endpoint
    echo ""
    echo "Attempting to fetch /metrics endpoint..."
    METRICS_OUTPUT=$(kubectl exec -n $NAMESPACE_LLMD "$POD_NAME" -- curl -s -m 5 http://localhost:8000/metrics 2>&1)
    if [ $? -eq 0 ] && [ -n "$METRICS_OUTPUT" ]; then
        print_success "Metrics endpoint is accessible"
        echo ""
        echo "First 20 lines of metrics:"
        echo "$METRICS_OUTPUT" | head -20

        # Check for vLLM metrics
        echo ""
        echo "Checking for vLLM metrics:"
        VLLM_METRICS=$(echo "$METRICS_OUTPUT" | grep -c "^vllm:" || echo "0")
        if [ "$VLLM_METRICS" -gt 0 ]; then
            print_success "Found $VLLM_METRICS vLLM metric lines"
            echo ""
            echo "Sample vLLM metrics:"
            echo "$METRICS_OUTPUT" | grep "^vllm:" | head -10
        else
            print_error "No vLLM metrics found! Controller expects vllm:* metrics"
            echo ""
            echo "Available metrics (first 10):"
            echo "$METRICS_OUTPUT" | grep -E "^[a-z_]+" | head -10
        fi

        # Check for model_name label
        echo ""
        echo "Checking for model_name label in metrics:"
        MODEL_LABEL_COUNT=$(echo "$METRICS_OUTPUT" | grep -c 'model_name=' || echo "0")
        if [ "$MODEL_LABEL_COUNT" -gt 0 ]; then
            print_success "Found model_name label in metrics"
            echo ""
            echo "Sample metrics with model_name:"
            echo "$METRICS_OUTPUT" | grep 'model_name=' | head -5

            # Check what model_name value is used
            echo ""
            echo "Checking model_name value:"
            MODEL_VALUE=$(echo "$METRICS_OUTPUT" | grep -o 'model_name="[^"]*"' | head -1)
            if [ -n "$MODEL_VALUE" ]; then
                echo "Found: $MODEL_VALUE"
                # Check if it matches expected value (for llm-d-sim tests)
                if echo "$MODEL_VALUE" | grep -q "Meta-Llama"; then
                    print_success "Model name looks correct (contains 'Meta-Llama')"
                elif echo "$MODEL_VALUE" | grep -q "ms-sim-llm-d-modelservice"; then
                    print_error "Model name is 'ms-sim-llm-d-modelservice' (deployment label, not actual model!)"
                    echo "llm-d-sim should use the --model arg value, not the deployment label"
                else
                    print_warning "Model name is: $MODEL_VALUE"
                    echo "Verify this matches the model in VariantAutoscaling spec"
                fi
            fi
        else
            print_error "No model_name label found in metrics!"
            echo "Controller requires model_name label on all vLLM metrics"
        fi

        # Check for namespace label
        echo ""
        echo "Checking for namespace label in metrics:"
        NAMESPACE_LABEL_COUNT=$(echo "$METRICS_OUTPUT" | grep -c 'namespace=' || echo "0")
        if [ "$NAMESPACE_LABEL_COUNT" -gt 0 ]; then
            print_success "Found namespace label in metrics"
            NAMESPACE_VALUE=$(echo "$METRICS_OUTPUT" | grep -o 'namespace="[^"]*"' | head -1)
            echo "Value: $NAMESPACE_VALUE"
        else
            print_warning "No namespace label found in metrics"
            echo "Controller will fallback to query without namespace label (this is OK)"
        fi
    else
        print_error "Failed to access metrics endpoint"
        echo "Error: $METRICS_OUTPUT"
    fi
else
    print_error "No running pods found to test metrics endpoint"
fi

# 4. Check Services
print_header "4. Checking Services in $NAMESPACE_LLMD"
if kubectl get svc -n $NAMESPACE_LLMD &>/dev/null; then
    SVC_COUNT=$(kubectl get svc -n $NAMESPACE_LLMD --no-headers 2>/dev/null | wc -l)
    if [ "$SVC_COUNT" -eq 0 ]; then
        print_warning "No services found"
    else
        print_success "Found $SVC_COUNT service(s)"
        echo ""
        kubectl get svc -n $NAMESPACE_LLMD -o wide
    fi
fi

# 5. Check ServiceMonitors
print_header "5. Checking ServiceMonitors"
if kubectl get servicemonitor -n $NAMESPACE_MONITORING &>/dev/null 2>&1; then
    # Count ServiceMonitors (both llm-d and vllme)
    SM_COUNT=$(kubectl get servicemonitor -n $NAMESPACE_MONITORING --no-headers 2>/dev/null | grep -E "(llm-d|vllme)" | wc -l | tr -d ' ')
    # Ensure SM_COUNT is a valid integer
    SM_COUNT=${SM_COUNT:-0}

    if [ "$SM_COUNT" -eq 0 ]; then
        print_warning "No llm-d or vllme ServiceMonitors found in $NAMESPACE_MONITORING"
        echo ""
        echo "All ServiceMonitors:"
        kubectl get servicemonitor -n $NAMESPACE_MONITORING
    else
        print_success "Found $SM_COUNT ServiceMonitor(s) for e2e tests"
        echo ""
        kubectl get servicemonitor -n $NAMESPACE_MONITORING | grep -E "(llm-d|vllme)"

        # Show details of first ServiceMonitor
        echo ""
        SM_NAME=$(kubectl get servicemonitor -n $NAMESPACE_MONITORING -o name 2>/dev/null | grep -E "(llm-d|vllme)" | head -1 | cut -d'/' -f2)
        if [ -n "$SM_NAME" ]; then
            echo "Details of ServiceMonitor: $SM_NAME"
            kubectl get servicemonitor -n $NAMESPACE_MONITORING "$SM_NAME" -o yaml | grep -A 10 "spec:"
        fi
    fi
else
    print_error "Cannot access ServiceMonitors (CRD might not be installed)"
fi

# 6. Check Prometheus is running
print_header "6. Checking Prometheus"
PROM_POD=$(kubectl get pods -n $NAMESPACE_MONITORING -l app.kubernetes.io/name=prometheus --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
if [ -n "$PROM_POD" ]; then
    print_success "Prometheus pod is running: $PROM_POD"
else
    print_error "Prometheus pod not found or not running"
    echo ""
    echo "Prometheus pods:"
    kubectl get pods -n $NAMESPACE_MONITORING -l app.kubernetes.io/name=prometheus
fi

# 7. Query Prometheus for vLLM metrics (requires port-forward in background)
print_header "7. Querying Prometheus for vLLM Metrics"
if [ -n "$PROM_POD" ]; then
    echo "Setting up temporary port-forward to Prometheus..."
    # Find an available port (9090 might be in use in CI)
    PROM_PORT=9090
    kubectl port-forward -n $NAMESPACE_MONITORING "pod/$PROM_POD" $PROM_PORT:9090 &>/dev/null &
    PF_PID=$!

    # Wait for port-forward to be ready (with timeout)
    echo "Waiting for port-forward to be ready..."
    for i in {1..10}; do
        if curl -sk "https://localhost:$PROM_PORT/-/ready" &>/dev/null; then
            print_success "Port-forward ready"
            break
        fi
        sleep 1
    done

    # Query for vLLM metrics
    echo "Querying: vllm:request_success_total"
    QUERY_RESULT=$(curl -sk --max-time 10 "https://localhost:$PROM_PORT/api/v1/query?query=vllm:request_success_total" 2>&1)

    if echo "$QUERY_RESULT" | grep -q '"status":"success"'; then
        # Count results without jq or python
        RESULT_COUNT=$(echo "$QUERY_RESULT" | grep -o '"result":\[' | wc -l)
        RESULT_ITEMS=$(echo "$QUERY_RESULT" | grep -o '"__name__":"vllm:request_success_total"' | wc -l)

        if [ "$RESULT_ITEMS" -gt 0 ]; then
            print_success "Prometheus query successful - found $RESULT_ITEMS metric(s)"
            echo ""
            echo "Query result (raw):"
            echo "$QUERY_RESULT"
        else
            print_warning "Prometheus query successful but returned no results"
            echo ""
            echo "This means Prometheus is not scraping vLLM metrics from llm-d-sim pods"
            echo "Raw response:"
            echo "$QUERY_RESULT"
        fi
    else
        print_error "Prometheus query failed"
        echo "Response: $QUERY_RESULT"
    fi

    # Kill port-forward
    kill $PF_PID 2>/dev/null
    wait $PF_PID 2>/dev/null
else
    print_warning "Skipping Prometheus queries - Prometheus pod not available"
fi

# 8. Check VariantAutoscaling resources
print_header "8. Checking VariantAutoscaling Resources"
VA_COUNT=$(kubectl get variantautoscaling -n $NAMESPACE_LLMD --no-headers 2>/dev/null | wc -l)
if [ "$VA_COUNT" -eq 0 ]; then
    print_warning "No VariantAutoscaling resources found in $NAMESPACE_LLMD"
else
    print_success "Found $VA_COUNT VariantAutoscaling resource(s)"
    echo ""
    kubectl get variantautoscaling -n $NAMESPACE_LLMD -o wide

    # Check status of each VA
    echo ""
    echo "Checking VariantAutoscaling statuses:"
    for va in $(kubectl get variantautoscaling -n $NAMESPACE_LLMD -o name 2>/dev/null); do
        VA_NAME=$(echo "$va" | cut -d'/' -f2)
        echo ""
        echo "--- $VA_NAME ---"

        DESIRED_REPLICAS=$(kubectl get "$va" -n $NAMESPACE_LLMD -o jsonpath='{.status.desiredOptimizedAlloc.numReplicas}' 2>/dev/null || echo "N/A")
        CURRENT_REPLICAS=$(kubectl get "$va" -n $NAMESPACE_LLMD -o jsonpath='{.status.currentAlloc.numReplicas}' 2>/dev/null || echo "N/A")

        echo "Current Replicas: $CURRENT_REPLICAS"
        echo "Desired Replicas: $DESIRED_REPLICAS"

        if [ "$DESIRED_REPLICAS" == "0" ] || [ "$DESIRED_REPLICAS" == "N/A" ]; then
            print_error "DesiredOptimizedAlloc.NumReplicas is $DESIRED_REPLICAS - Controller hasn't processed this VA!"
        else
            print_success "VA has been processed by controller"
        fi

        # Check conditions
        echo ""
        echo "Conditions:"
        CONDITIONS=$(kubectl get "$va" -n $NAMESPACE_LLMD -o jsonpath='{.status.conditions}' 2>/dev/null)
        if [ -n "$CONDITIONS" ] && [ "$CONDITIONS" != "[]" ]; then
            echo "$CONDITIONS"
        else
            echo "No conditions set"
        fi
    done
fi

# 9. Check Controller logs
print_header "9. Checking Controller Logs (last 50 lines)"
CONTROLLER_POD=$(kubectl get pods -n $NAMESPACE_CONTROLLER -l app.kubernetes.io/name=workload-variant-autoscaler --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
if [ -n "$CONTROLLER_POD" ]; then
    print_success "Controller pod: $CONTROLLER_POD"
    echo ""
    echo "Recent logs:"
    kubectl logs -n $NAMESPACE_CONTROLLER "$CONTROLLER_POD" --tail=50 2>/dev/null | grep -E "(ERROR|WARN|Metrics|variantAutoscaling|skipping)" || echo "No relevant log entries found"

    echo ""
    echo "Checking for 'Metrics unavailable' errors:"
    METRICS_UNAVAIL=$(kubectl logs -n $NAMESPACE_CONTROLLER "$CONTROLLER_POD" --tail=100 2>/dev/null | grep -c "Metrics unavailable" | tr -d ' ')
    # Ensure METRICS_UNAVAIL is a valid integer
    METRICS_UNAVAIL=${METRICS_UNAVAIL:-0}
    if [ "$METRICS_UNAVAIL" -gt 0 ]; then
        print_error "Found $METRICS_UNAVAIL 'Metrics unavailable' log entries"
        echo ""
        kubectl logs -n $NAMESPACE_CONTROLLER "$CONTROLLER_POD" --tail=100 2>/dev/null | grep "Metrics unavailable"
    else
        print_success "No 'Metrics unavailable' errors in recent logs"
    fi
else
    print_error "Controller pod not found or not running"
    echo ""
    kubectl get pods -n $NAMESPACE_CONTROLLER
fi

# 10. Check ConfigMaps
print_header "10. Checking ConfigMaps"
echo "Service Classes ConfigMap:"
if kubectl get configmap service-classes-config -n $NAMESPACE_CONTROLLER &>/dev/null; then
    print_success "service-classes-config exists"
    echo ""
    echo "Models in premium service class:"
    kubectl get configmap service-classes-config -n $NAMESPACE_CONTROLLER -o jsonpath='{.data.premium\.yaml}' | grep "model:" || echo "Cannot parse"
else
    print_error "service-classes-config ConfigMap not found"
fi

echo ""
echo "Checking for 'unsloth/Meta-Llama-3.1-8B' in service-classes-config:"
if kubectl get configmap service-classes-config -n $NAMESPACE_CONTROLLER -o yaml | grep -q "unsloth/Meta-Llama-3.1-8B"; then
    print_success "Model 'unsloth/Meta-Llama-3.1-8B' found in service-classes-config"
else
    print_error "Model 'unsloth/Meta-Llama-3.1-8B' NOT found in service-classes-config!"
    echo "This will cause controller to skip VAs with this model"
fi

# 11. Summary
print_header "11. Diagnostic Summary"
echo ""
echo "Key checks:"
echo "  1. Are llm-d-sim pods running? Check section 2"
echo "  2. Do llm-d-sim pods expose vLLM metrics? Check section 3"
echo "  3. Is Prometheus scraping the metrics? Check section 7"
echo "  4. Is the model in service-classes-config? Check section 10"
echo "  5. Is the controller processing VAs? Check sections 8 and 9"
echo ""
echo "Common issues:"
echo "  - If vLLM metrics are missing: llm-d-sim may not be compatible"
echo "  - If Prometheus returns no results: ServiceMonitor configuration issue"
echo "  - If DesiredReplicas = 0: Controller can't find metrics or model SLO"
echo "  - Check controller logs for 'Metrics unavailable' or 'skipping optimization'"
echo ""
print_header "Diagnostics Complete"
