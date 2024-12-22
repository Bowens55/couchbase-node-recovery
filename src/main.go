/*
This package is for handling the recovery and rebalance of node failures
in a couchbase cluster. It will continuously run until a node is found to
be failed out/unhealthy. If we rebalance too many times in a given window we will back off
as there is likely a larger issue at hand.
*/
package main

import (
	"log"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

var (
	password, username, clusterUrl string
)

func main() {
	godotenv.Load()

	password = os.Getenv("CB_PASSWORD")
	username = os.Getenv("CB_USERNAME")
	if password == "" || username == "" {
		log.Fatal("Missing couchbase username of password env var.")
	}

	clusterUrl = os.Getenv("CB_URL")
	if clusterUrl == "" {
		log.Fatal("Set the env var for CB_URL.")
	}

	// controls whether we take action or not.
	dryRunEnv := os.Getenv("DRY_RUN")
	isDryRun, err := strconv.ParseBool(dryRunEnv)
	if err != nil {
		log.Println("DRY_RUN empty OR Failed to parse env var to bool for DRY_RUN.")
		isDryRun = false
	}

	log.Printf("isDryRun is currently set to: %v.", isDryRun)

	clusterRebalanceCount := 0
	nodeRebalanceCount := make(map[string]int)
	timesForComparison := map[string]*time.Time{
		"cluster":    {},
		"singleNode": {},
	}
	// enter infinite loop to constantly check cluster status
	for {
		// if we are within 2 hours and we have rebalanced cluster as a whole more than 3
		// times, back off for 4 hours.
		handleClusterRebalanceBackoff(
			timesForComparison["cluster"],
			&clusterRebalanceCount,
		)

		handleNodeRebalanceBackoff(
			timesForComparison["singleNode"],
			&nodeRebalanceCount,
		)

		cbNodesState, err := getNodesState()
		if err != nil {
			log.Fatal(err)
		}

		failedNode := cbNodesState.getFailedNode()

		if failedNode.Hostname != "" {
			if failedNode.Status != "healthy" {
				log.Printf("%s isn't healthy, trying again later.", failedNode.Hostname)
				continue
			}

			_, err := recoverNode(failedNode, isDryRun)
			if err != nil {
				log.Println("Unable to recover node back into cluster. Skipping rebalance.")
				continue
			}

			_, err = rebalanceCluster(cbNodesState.getAllNodes(), isDryRun)
			if err != nil {
				log.Println("Failed to rebalance cluster.", err)
			}

			// increment counters
			nodeRebalanceCount[failedNode.Hostname]++
			clusterRebalanceCount++

			// kick off timers once we actually rebalance the cluster.
			for _, startTime := range timesForComparison {
				if (*startTime).IsZero() {
					*startTime = time.Now()
				}
			}
		}
		// sleep in between state checks
		time.Sleep(1 * time.Minute)
	}
}
