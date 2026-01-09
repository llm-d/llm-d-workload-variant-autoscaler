# Workload-Variant-Autoscaler Documentation

Welcome to the WVA documentation! This directory contains comprehensive guides for users, developers, and operators.

## Documentation Structure

### User Guide

Getting started and using WVA:

- **[Installation Guide](user-guide/installation.md)** - Installing WVA on your cluster
- **[Configuration](user-guide/configuration.md)** - Configuring WVA for your workloads
- **[CRD Reference](user-guide/crd-reference.md)** - Complete API reference for VariantAutoscaling
- **[Multi-Controller Isolation](user-guide/multi-controller-isolation.md)** - Running multiple WVA controller instances

### Tutorials

Step-by-step guides:

- **[Quick Start Demo](tutorials/demo.md)** - Getting started with WVA
- **[Parameter Estimation](tutorials/parameter-estimation.md)** - Estimating model parameters
- **[vLLM Samples](tutorials/vllm-samples.md)** - Working with vLLM servers
- **[GuideLLM Sample](tutorials/guidellm-sample.md)** - Using GuideLLM for benchmarking

### Integrations

Integration with other systems:

- **[HPA Integration](integrations/hpa-integration.md)** - Using WVA with Horizontal Pod Autoscaler
- **[KEDA Integration](integrations/keda-integration.md)** - Using WVA with KEDA
- **[Prometheus Integration](integrations/prometheus.md)** - Custom metrics and monitoring

### Design & Architecture

Understanding how WVA works:

- **[Modeling & Optimization](design/modeling-optimization.md)** - Queue theory models and optimization algorithms
- **[Controller Behavior](design/controller-behavior.md)** - Event handling and reconciliation behavior
- **[Architecture Limitations](design/architecture-limitations.md)** - **Important:** Model architecture assumptions and limitations (READ THIS if using HSSM, MoE, or non-standard architectures)
- **[Architecture Diagrams](design/diagrams/)** - System architecture and workflows

### Developer Guide

Contributing to WVA:

- **[Development Setup](developer-guide/development.md)** - Setting up your dev environment
- **[Package Documentation](developer-guide/package-documentation.md)** - Understanding the codebase structure
- **[Testing](developer-guide/testing.md)** - Running tests and CI workflows
- **[Agentic Workflows](developer-guide/agentic-workflows.md)** - AI-powered automation workflows
- **[Debugging](developer-guide/debugging.md)** - Debugging techniques and tools
- **[Contributing](../CONTRIBUTING.md)** - How to contribute to the project

## Getting Started

New to WVA? Start here:

- **[Quick Start Guide](quickstart.md)** - Get running in 10 minutes
- **[Installation Guide](user-guide/installation.md)** - Detailed installation instructions
- **[Quick Start Demo](tutorials/demo.md)** - Step-by-step walkthrough

## Quick Links

- [Main README](../README.md)
- [Kubernetes Deployment](../deploy/kubernetes/README.md)
- [OpenShift Deployment](../deploy/openshift/README.md)
- [Local Development with Kind Emulator](../deploy/kind-emulator/README.md)

## Key Topics

### Core Concepts
- [Saturation-Based Scaling](saturation-scaling-config.md) - Understanding the optimization model
- [Saturation Analyzer](saturation-analyzer.md) - Deep dive into saturation analysis
- [Metrics & Health Monitoring](metrics-health-monitoring.md) - Monitor WVA performance

### Advanced Topics
- [Parameter Estimation](tutorials/parameter-estimation.md) - Tuning for your models
- [Cost Optimization](user-guide/configuration.md#cost-optimization) - Minimizing infrastructure costs
- [Multi-Tenant Deployments](user-guide/multi-controller-isolation.md) - Large-scale architecture

## Additional Resources

- [Community Proposal](https://docs.google.com/document/d/1n6SAhloQaoSyF2k3EveIOerT-f97HuWXTLFm07xcvqk/edit)
- [llm-d Infrastructure](https://github.com/llm-d-incubation/llm-d-infra)
- [API Proposal](https://docs.google.com/document/d/1j2KRAT68_FYxq1iVzG0xVL-DHQhGVUZBqiM22Hd_0hc/edit)
- [Design Discussions](https://docs.google.com/document/d/1iGHqdxRUDpiKwtJFr5tMCKM7RF6fbTfZBL7BTn6UkwA/edit?tab=t.0#heading=h.mdte0lq44ul4)

## Need Help?

- Check the [FAQ](user-guide/faq.md)
- Review the [Troubleshooting Guide](user-guide/troubleshooting.md)
- Open a [GitHub Issue](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues)
- Join [community meetings](https://join.slack.com/share/enQtOTg1MzkwODExNDI5Mi02NWQwOWEwOWM4Y2Y3MTc4OTQyY2Y1ZDVlZmU2MjBmZDUwNjJhZGM3MjY4ZTQ5OTdjZjgzMmI0NjI0ZTBhZTM4)

---

**Note:** Documentation is continuously being improved. If you find errors or have suggestions, please open an issue or submit a PR!

