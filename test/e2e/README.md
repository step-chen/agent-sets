# End-to-End (E2E) Testing Guide

This directory contains the End-to-End (E2E) test suite for the PR Review Automation system. The tests verify the complete pipeline by simulating real Webhook requests, executing live LLM inference, and fetching actual MCP data.

## Directory Structure

```text
test/e2e/
├── requests/           # Webhook Payloads (JSON)
│   ├── 01_pr.json
│   └── 02_chunked.json
├── config.test.yaml    # E2E-specific configuration
├── .env                # E2E-specific environment variables (API Keys, etc.)
├── README.md           # This document
├── README.zh.md        # Chinese documentation
└── e2e_main_test.go    # The test runner
```

## How It Works

1.  **File-Driven**: The test runner reads all `*.json` files from the `requests/` directory in alphabetical order.
2.  **Full Simulation**: Each JSON file simulates a POST request to the `/webhook` endpoint, triggering the complete processing flow (Debounce -> WorkerPool -> PR Review Pipeline).
3.  **Live Interaction**:
    - **LLM**: Executes real online model requests.
    - **MCP**: Executes real read operations (Get Diff, Get File Content).
4.  **Security Interception**: Uses `InterceptingTransport` to block `bitbucket_add_pull_request_comment` calls, preventing tests from actually posting comments to Bitbucket.
5.  **Unified Report**: After all tests complete, a summary of all intercepted comments is printed to the console.

## How to Build Your Own Test

### 1. Capture a Payload

Capture a real Webhook Payload from Bitbucket or production logs. It can be `pr:opened`, `pr:from_ref_updated`, or any supported event.

### 2. Add to `requests/`

Save the payload as a `.json` file in `test/e2e/requests/`.

> [!TIP]
> Use a numeric prefix (e.g., `03_feature_x.json`) to control the execution order. Since PR processing uses a key-lock, requests will be processed serially.

### 3. Configure Environment & Config

Create your local configuration files in `test/e2e/`. These files are ignored by Git:

- **.env**: Copy from root `.env.example` or create new (must contain `LLM_API_KEY`).
- **config.test.yaml**: Copy from root `config.example.yaml` and adjust settings for E2E (e.g., set `server.concurrency_limit: 1`).

```bash
# Example setup
cp ../../.env.example .env
cp ../../config.example.yaml config.test.yaml
```

### 4. Run the Test

Run the test using the `e2e` tag. It is recommended to use `-count=1` to disable caching and force LLM requests:

```bash
go test -v -count=1 -tags=e2e ./test/e2e
```

## Notes

- **Timeout**: real LLM calls have a 10-minute timeout. Please be patient for large PRs.
- **Read-Only**: As long as the `InterceptingTransport` logic is intact, no data will be written to external systems.
- **Dependencies**: Requires correctly configured and accessible MCP servers.
