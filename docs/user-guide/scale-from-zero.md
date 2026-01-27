# Scale from Zero

> **Note**: This documentation is a placeholder. Detailed documentation for the Scale-from-Zero feature is coming soon.

## Overview

The Scale-from-Zero feature automatically scales model deployments from zero replicas back up when new requests arrive. It works in conjunction with [Scale-to-Zero](scale-to-zero.md) to provide complete lifecycle management for idle workloads.

### How It Works

Scale-from-Zero monitors the EPP (End Point Picker) queue for pending requests:

1. **Monitors EPP queue**: Watches `inference_extension_flow_control_queue_size` metric
2. **Detects pending requests**: Identifies when requests are queued for models at zero replicas
3. **Triggers scale-up**: Automatically scales the model deployment to handle incoming traffic

### Key Features

- **Fast response**: Reacts quickly to incoming requests for idle models
- **Queue-aware**: Uses EPP flow control metrics to detect demand
- **Integrated**: Works seamlessly with the saturation-based scaling pipeline

## Configuration

*Documentation coming soon.*

## Related Documentation

- [Scale to Zero](scale-to-zero.md) - Automatic scale-down for idle models
- [Saturation Scaling Configuration](../saturation-scaling-config.md) - Configure saturation thresholds