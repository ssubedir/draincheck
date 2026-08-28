package config

const Template = `# Draincheck tests the startup and graceful-shutdown behavior of a built image.
version: 1

target:
  image: {{IMAGE}}
  container_port: {{PORT}}
  environment: {}

readiness:
  driver: http # http, grpc, or exec
  # container_port: 8081 # omitted inherits target.container_port
  path: /ready
  success_status: 200
  # grpc:
  #   service: "" # empty checks overall server health
  # exec:
  #   command: ["/app/healthcheck", "--ready"] # runs inside the target container; exit 0 means ready
  startup_timeout: 20s
  interval: 200ms

traffic:
  driver: http # http, command, or grpc
  # container_port: 50051 # omitted inherits target.container_port
  request:
    method: GET
    path: /work?delay=2s
    headers: {}
    # body: '{"operation":"draincheck"}'
    # body_file: ./testdata/draincheck-request.json
    # success_statuses: [200, 202] # omitted means 200-399
  # command:
  #   executable: ./ci/draincheck-probe
  #   args: []
  #   environment: {}
  #   working_directory: .
  # grpc:
  #   method: example.jobs.v1.Worker/Run
  #   request: '{"job_id":"draincheck"}'
  #   # request_file: ./testdata/grpc-request.json
  #   metadata: {}
  #   # descriptor_set: ./api.protoset # omitted uses server reflection
  #   expected_codes: [OK]
  count: 5
  concurrency: 5
  shutdown_after: 500ms
  request_timeout: 10s
  post_signal:
    policy: disabled # disabled, accept, or reject
    delay: 0s
    count: 1

streaming:
  sse:
    enabled: false
    # container_port: 8081 # omitted inherits target.container_port
    path: /events
    headers: {}
    initial_event: ready
    terminal_event: shutdown
    establish_timeout: 2s
    close_timeout: 5s
  websocket:
    enabled: false
    # container_port: 8081 # omitted inherits target.container_port
    path: /ws
    headers: {}
    subprotocols: []
    terminal_message: shutdown # empty means no terminal message required
    close_code: 1000
    establish_timeout: 2s
    close_timeout: 5s
  grpc:
    enabled: false
    # container_port: 50051 # omitted inherits target.container_port
    method: example.jobs.v1.Worker/Watch
    request: '{"job_id":"draincheck"}'
    metadata: {}
    # descriptor_set: ./api.protoset # omitted uses server reflection
    minimum_messages: 1
    expected_code: OK
    establish_timeout: 2s
    close_timeout: 5s

telemetry:
  traces:
    enabled: false
    minimum_correlated_spans: 1
    flush_timeout: 2s
  metrics:
    enabled: false
    minimum_data_points: 1
    flush_timeout: 2s

repeat:
  budgets:
    # startup_ready_p95: 2s
    # readiness_withdrawal_p95: 750ms
    # container_exit_p95: 5s

shutdown:
  signal: SIGTERM
  deadline: 15s
  # pre_stop: # runs inside the container before the signal; time counts against deadline
  #   exec:
  #     command: ["/app/pre-stop"]

assertions:
  readiness_withdrawn_within: 2s
  inflight_requests_complete: true
  max_failed_requests: 0
  exit_code: 0
  forbid_force_kill: true
`
