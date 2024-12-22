# dkr-couchbase-auto-recover

A Golang application that automatically monitors and recovers a single failed node in your Couchbase cluster. The tool continuously polls the Couchbase API at configurable intervals to detect node failures and automatically initiates recovery and rebalancing procedures.

# Safety Features

Only handles single node failures - if multiple nodes fail simultaneously, the tool will pause operations
Implements rate limiting for rebalancing operations to prevent cluster instability

## Local setup

You can run locally by configuring a .env file with the following:

```bash
CB_USERNAME=user
CB_PASSWORD=passw
CB_URL=your_cluster_url  # Optional
```

Then run `go run main.go`
