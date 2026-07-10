---
Name: Bug report
About: Report something that isn't working as expected
Title: "[Bug]: "
Labels: bug
Assignees: ''
---

## Description
A clear, concise description of what the bug is.

## Terraform configuration
```hcl
# Minimal .tf snippet that reproduces the issue
```

## Steps to reproduce
1. Go to '...'
2. Run '...'
3. See error

## Expected behavior
What you expected to happen.

## Actual behavior
What actually happened. Include full error messages / stack traces if applicable.

## Debug output
Run with `TF_LOG=DEBUG` and paste the relevant excerpt (redact any secrets, tokens, or endpoint URLs):

```
paste here
```

## Environment
- Provider version:
- Terraform version (`terraform version`):
- Workflow: Docker (`README.docker.md`) / local Make install (`README.make.md`)
- OS:

## Additional context
Anything else that might help — related resources, whether this worked before, workarounds tried, etc.

## Possible Fix (optional)
If you have an idea of what's causing this or how to fix it, describe it here.
