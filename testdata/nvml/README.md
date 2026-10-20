Answers of greenlens-nvml, one JSON object per line: the answer to a probe, then the answers to successive reads.

- driver-595.jsonl was recorded on an NVIDIA GeForce RTX 3050 with driver 595.99.02, which has an energy counter. Process IDs are replaced.
- driver-470-two-gpus.jsonl describes a server with a Pascal card, which reports power but has no energy counter, and a Volta card, which has one, as documented in nvml.h for nvmlDeviceGetTotalEnergyConsumption. Values are arbitrary.
