package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type nodesResponse struct {
	Name  string    `json:"name"`
	Nodes []nodeMap `json:"nodes"`
}

type nodeMap struct {
	ClusterMembership string
	RecoveryType      string
	Status            string
	Hostname          string
	OtpNode           string
	Services          []string
}

// gets the current state of the cluster
// returns a list of nodes and their statuses.
func getNodesState() (result nodesResponse, err error) {
	nodesResponseBody, _, err := doCbRequest("/pools/default", nil)
	if err != nil {
		log.Fatal(err)
	}

	if err := json.Unmarshal(nodesResponseBody, &result); err != nil { // Parse []byte to go struct pointer
		log.Println("Cannot unmarshal JSON.")
		return result, err
	}

	return result, err
}

// do request with proper headers/forms etc...defaulting to get if form data not set
func doCbRequest(apiPath string, form url.Values) ([]byte, int, error) {
	client := &http.Client{}
	reqURL := clusterUrl + apiPath
	var request *http.Request
	var err error
	if form != nil {
		request, err = http.NewRequest("POST", reqURL, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, http.StatusInternalServerError, fmt.Errorf("failed to create request: %w", err)
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		var bodyReader io.Reader
		request, err = http.NewRequest("GET", reqURL, bodyReader)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to create request: %w", err)
		}
	}

	request.SetBasicAuth(username, password)
	response, err := client.Do(request)
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("failed to execute request: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, response.StatusCode, fmt.Errorf("failed to read response: %w", err)
	}

	return responseBody, response.StatusCode, nil
}

// recover node before rebalancing, otherwise the node is removed.
func recoverNode(node nodeMap, isDryRun bool) ([]byte, error) {
	form := url.Values{}
	form.Set("otpNode", node.OtpNode)
	form.Set("recoveryType", "full")

	if !isDryRun {
		log.Printf("recovering node %s.\n", node.Hostname)
		responseBody, statusCode, err := doCbRequest("/controller/setRecoveryType", form)
		if err != nil {
			return responseBody, err
		}

		if statusCode != http.StatusOK {
			return responseBody, fmt.Errorf("HTTP request failed with status: %d and body %s", statusCode, responseBody)
		}

		return responseBody, nil
	}
	log.Printf("Would have recovered node: %s.\n", node.Hostname)
	return nil, nil
}

// rebalance cluster by passing the list of all nodes in the form data
// if full list is not passed, post will fail OR if partial list, the nodes that weren't
// passed will be removed.
func rebalanceCluster(
	nodeNames []string,
	isDryRun bool,
) ([]byte, error) {
	if len(nodeNames) == 0 {
		return nil, fmt.Errorf("no nodes provided for rebalance")
	}

	log.Printf("List of nodes we are passing for rebalance: \n%v\n\n", nodeNames)

	formattedNodes := strings.Join(nodeNames, ",")
	form := url.Values{}
	form.Set("knownNodes", formattedNodes)

	if !isDryRun {
		responseBody, statusCode, err := doCbRequest("/controller/rebalance", form)
		if err != nil {
			return responseBody, err
		}

		if statusCode != http.StatusOK {
			return responseBody, fmt.Errorf("HTTP request failed with status: %d and body %s", statusCode, responseBody)
		}

		return responseBody, nil
	}

	log.Printf("Would have rebalanced the cluster.\n")
	return nil, nil
}

// get a single failed node, otherwise return an empty nodeMap struct.
// if more than 1, we will pass empty struct so that we backoff.
func (resp nodesResponse) getFailedNode() nodeMap {
	var failedNode nodeMap
	count := 0

	for _, node := range resp.Nodes {
		if node.ClusterMembership != "active" {
			count++
			failedNode = node
		}
	}

	if count > 1 {
		log.Println("Too many failed nodes, backing off for 5 minutes.")
		return nodeMap{}
	} else if count == 0 {
		log.Println("Cluster in a healthy state, sleeping for 5 minutes.")
		return nodeMap{}
	}

	return failedNode
}

// get nodes in the the proper OTP format to pass for rebalancing.
func (resp nodesResponse) getAllNodes() []string {
	otpNodes := make([]string, len(resp.Nodes))
	for i, node := range resp.Nodes {
		otpNodes[i] = node.OtpNode
	}
	return otpNodes
}

// check the map for more than N number of rebalances
// return bool but also the node that has more than N rebalances.
func checkForMultipleRebalances(m map[string]int, valueToCompare int) (bool, string) {
	for i, value := range m {
		if value >= valueToCompare {
			log.Printf("%s has been rebalanced more than %d times.\n", i, valueToCompare)
			return true, i
		}
	}
	return false, ""
}

// if we are within 2 hours of duration and we have rebalanced cluster as a whole more than 3
// times, back off for 4 hours.
func handleClusterRebalanceBackoff(
	startTime *time.Time,
	count *int,
) {
	// if timer hasn't started (because we haven't rebalanced yet)
	// just return
	if (*startTime).IsZero() {
		return
	}

	timeSince := time.Since(*startTime)
	if timeSince >= (2 * time.Hour) {
		// reset timers if we have passed 2 hours
		*count = 0
		*startTime = time.Now()
		log.Println("Resetting timer for cluster rebalance count as we have passed the 2 hour mark.")
		return
	} else if *count >= 3 {
		// if we have rebalanced more than 3 times
		// reset timers and backoff for 4 hours
		*count = 0
		*startTime = time.Time{} // reset to null time since we are backing off.
		log.Println("Too many CLUSTER rebalance attempts, backing off for 4 hours.")
		time.Sleep(4 * time.Hour)
	}
}

// if we are within a 2 hour duration and a single node has failed out
// 2 or more times, back off for 4 hours.
func handleNodeRebalanceBackoff(startTime *time.Time, m *map[string]int) {
	// if timer hasn't started (because we haven't rebalanced yet)
	// just return
	if (*startTime).IsZero() {
		return
	}
	// checking for 2+ rebalances
	isTooManyRebalances, node := checkForMultipleRebalances(*m, 2)
	timeSince := time.Since(*startTime)
	if timeSince >= (2 * time.Hour) {
		// reset timers and count
		*m = make(map[string]int)
		*startTime = time.Now()
		log.Println("Clearing node to rebalance count map and resetting timer.")
	} else if isTooManyRebalances {
		(*m)[node] = 0
		*startTime = time.Time{} // reset back to null time since we are backing off.
		log.Println("Too many SINGLE NODE rebalance attempts. Backing off for 4 hours.")
		time.Sleep(4 * time.Hour)
	}
}
