# Hypotheses: TCP Connection Lifecycle

### Hypothesis H1
Persistent connections should require fewer TCP connection establishments for the same number of application requests. Specifically, per-request mode will establish exactly `requests` connections, while persistent mode will establish exactly `concurrency` connections.

### Hypothesis H2
Persistent connections should generally improve throughput (RPS) relative to one-connection-per-request workloads because connection establishment overhead (3-way handshake) is amortized over many requests. The performance gap will be wider for smaller payload sizes where connection overhead dominates.

### Hypothesis H3
Connection behavior (and the penalty for short-lived connections) should become increasingly important as concurrency and request count increase, as the OS struggles to rapidly allocate and tear down ephemeral ports and socket descriptors.
