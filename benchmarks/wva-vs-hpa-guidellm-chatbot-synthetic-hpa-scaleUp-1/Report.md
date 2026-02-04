
## Establishing Baseline with HPA:

By benchmarking the HPA first, we establish a baseline for how a general-purpose tool handles the highly specific, non-linear behavior of LLMs. It solves the equation:

$$DesiredReplicas = \lceil CurrentReplicas \times \frac{CurrentMetricValue}{TargetValue} \rceil$$

It treats all pods in a deployment as identical units. It calculates a global average of the target metric across all pods and scales up or down to keep that average near the target.

**Metrics:** We focus on the two most common HPA thresholds (see for instance https://docs.cloud.google.com/kubernetes-engine/docs/best-practices/machine-learning/inference/autoscaling). These metrics are also used by saturation based wva, enabling a fair comparison between the two.

- Metric A (Queue Size). Target a fixed number of waiting requests (e.g., target value: 5).

- Metric B (KV Cache Utilization). Target a percentage of active processing time (e.g., target average utilization: 80).

**Note:** EPP drops only the sheddable requests (priority < 0). The saturation detection at EPP is bypassed for non-scheddable requests.

Ref:

- https://github.com/kubernetes-sigs/gateway-api-inference-extension/blob/main/site-src/concepts/priority-and-capacity.md#capacity

- https://github.com/kubernetes-sigs/gateway-api-inference-extension/blob/main/site-src/guides/troubleshooting.md#429-too-many-requests

### Benchmark 01: Saturation and Criticality Impact

#### Objective

To understand and compare the behavior of each autoscaler when faced with a compounding workload. For the initial experiment, we don’t enable the flow control.

#### Load Profile

The test sends load that resembles a step function. We specify such a load generator as follows:

**Duration:** Constant load for 20 minutes per step.

**Sequence:** 2 → 3 → 5 → 6 req/sec.

**Payload:** Average input tokens 4096 and average output tokens 1024.

#### Performance Metrics:

- Number of replicas

- Mean TTFT

- Error rate

#### HPA Configuration:

- Ensure that the deployment starts with one replica.

- **HPA:** Configure an HPA targeting the vLLM queue metric with a target AverageValue of 3.

    ```
    apiVersion: autoscaling/v2
        kind: HorizontalPodAutoscaler
        ...
        spec:
            behavior:
                scaleDown:
                    policies:
                    - periodSeconds: 60
                        type: Percent
                        value: 10
                    selectPolicy: Max
                    stabilizationWindowSeconds:
                scaleUp:
                    policies:
                    - periodSeconds: 60
                        type: Pods
                        value: 1
                    selectPolicy: Max
                    stabilizationWindowSeconds: 240
            maxReplicas: 10
            metrics:
            - external:
                metric:
                    name: vllm:num_requests_waiting
                    selector:
                    matchLabels:
                        namespace: <namespace>
                target:
                    averageValue: "3"
                    type: AverageValue
                type: External
            - external:
                metric:
                    name: vllm:kv_cache_usage_perc
                    selector:
                        matchLabels:
                            namespace: <namespace>
                target:
                    averageValue: "600m"
                    type: AverageValue
                type: External

            minReplicas: 1
            ...
    ```

#### Benchmark Results under HPA:

- Metrics observed from experiments:

    ![avg kv_cache_usage_perc & num_requests_waiting](hpa1_1.png)

- Expected Behaviour with HPA

    We observe that as the the load increases, due to increased pressure on kv cache, the number of requests waiting jump from 0 to 150. Recall that the configured average queue threshold value is 3. Applying HPA scaling formula

    $$ \lceil CurrentReplicas \times \frac{CurrentMetricValue}{TargetValue} \rceil = \lceil 1 \times \frac{150}{3} = 50$$

    provides the number of desired replicas as 50. Since the maximum number of replicas configured is 10, we only see a scale up to 10 replicas. Additionally, HPA should scale up 1 pod per minute as its configuration.

- Observed Behavior with HPA

    In the "Replicas set & ready", the green plot shows the number of replicas set in the deployment; the blue plot shows the number of replicas in ready state.
    ![Replica set & ready](hpa1_1_1.png)
    ![kv_cache_usage_perc for replicas](hpa1_2.png)
    ![num_requests_waiting for replicas](hpa1_3.png)

#### WVA Configuration:

- Ensure that the deployment starts with one replica.

- Configure the WVA with a queueLengthThreshold of 5 and a queueSpareTrigger of 3. Set the kv cache threshold to 0.8 and the kv spare trigger to 0.3.

- WVA Version v0.4.2
- Scaling policy
    ```
    apiVersion: autoscaling/v2
    kind: HorizontalPodAutoscaler
    ...
    spec:
        behavior:
            scaleDown:
            policies:
            - periodSeconds: 60
                type: Percent
                value: 10
            selectPolicy: Max
            stabilizationWindowSeconds: 240
            scaleUp:
            policies:
            - periodSeconds: 60
                type: Pods
                value: 1
            selectPolicy: Max
            stabilizationWindowSeconds: 240
        maxReplicas: 10
        metrics:
        - external:
            metric:
                name: wva_desired_replicas
                selector:
                    matchLabels:
                        namespace: <namespace>
            target:
                averageValue: "1"
                type: AverageValue
            type: External
        minReplicas: 1
        ...
    ```


#### Benchmark Results under WVA:

-  Metrics observed from experiments:

    ![avg kv_cache_usage & num_requests_waiting](wva1.png)

- Expected Behavior with WVA

    According to the saturation analyzer algorithm, one should expect to see scale up when the resource spare capacity falls below the spare capacity trigger. Hence, for our configuration, considering a non-saturated replica ( kv cache usage < 0.8), we expect a scale up when the kv cache usage exceeds 0.5.

- Observed Behavior with WVA

    We observe that around 13:26, the kv cache usage exceeds 0.5, triggering a scale up. Subsequent scale down occurs as the kv cache usage falls below 0.5.

    ![Replicas set & ready](wva2.png)
    ![kv_cache_usage_perc for replicas](wva3.png)
    ![num_requests_waiting for replicas](wva4.png)

#### Other Metric Results:
In the following charts, the red and yellow plots repesent WVA and HPA, respectively.

![failures](failures.png)

![ttft](ttft.png)
