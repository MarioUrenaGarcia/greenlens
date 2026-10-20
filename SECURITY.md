# Security policy

## Supported versions

Security fixes are made on the `main` branch and published in the next
release. Until version 1.0, only the latest release receives fixes.

## Reporting a vulnerability

Do not open a public issue. Report the problem privately through GitHub:
https://github.com/MarioUrenaGarcia/greenlens/security/advisories/new

Include, when possible:

- the GreenLens version (`greenlens version`) and how it was installed;
- the distribution, kernel version and hardware involved;
- the steps to reproduce the problem and its impact;
- any proof of concept, with secrets and personal data removed.

## What to expect

| Step | Time |
|---|---|
| Acknowledgement of the report | 5 business days |
| First assessment and severity | 15 business days |
| Fix or mitigation released | depends on severity; the goal is 90 days at most |

The report is handled confidentially. Once a fix is available, an advisory is
published with credit to the reporter, unless the reporter prefers to remain
anonymous.

## Scope

In scope: the `greenlens` binary and its subcommands, the packages and
container images published by the project, and the files under `deploy/`.
Out of scope: vulnerabilities in third-party backends such as vLLM, TGI,
llama.cpp or Ollama, which should be reported to their own projects.
