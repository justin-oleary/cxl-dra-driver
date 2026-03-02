---
name: Bug Report
about: Report a bug or unexpected behavior
title: "[BUG] "
labels: bug
assignees: ''
---

## Description

<!-- A clear description of the bug -->

## Steps to Reproduce

1.
2.
3.

## Expected Behavior

<!-- What you expected to happen -->

## Actual Behavior

<!-- What actually happened -->

## Environment

- **Kubernetes version:**
- **CXL DRA Driver version:**
- **Go version (if building from source):**
- **OS:**

## Logs

<details>
<summary>Controller logs</summary>

```
kubectl -n cxl-system logs deploy/cxl-dra-controller --tail=100
```

</details>

<details>
<summary>Node plugin logs</summary>

```
kubectl -n cxl-system logs ds/cxl-node-plugin --tail=100
```

</details>

## Additional Context

<!-- Any other relevant information -->
