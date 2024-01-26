package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/joho/godotenv"
	log "github.com/sirupsen/logrus"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const timeLayout = "2006-01-02 15:04:05"
const lastRunFile = "last_run.txt"
const interval = 2 * time.Minute
const firstRun = 10 * time.Minute

func main() {
	// Load environment variables from .env file
	err := godotenv.Load()
	if err != nil {
		log.Fatal(err)
	}

	log.Info("Starting fax_notify")
	for {
		// Get the last run time from file
		sinceTime := getLastRunTime()

		// Run the journalctl command and parse the output
		log.Info("Running journalctl")
		output := runJournalctl(sinceTime)
		parseOutput(output)

		// Update the last run time to the current time
		updateLastRunTime()

		// Wait for 2 minutes before the next run
		time.Sleep(interval)
	}
}

// getLastRunTime retrieves the last run time from a file.
func getLastRunTime() time.Time {
	content, err := ioutil.ReadFile(lastRunFile)
	if err != nil {
		// If there's an error (e.g., file doesn't exist), default to 2 minutes ago
		return time.Now().Add(-firstRun)
	}

	lastRunStr := string(content)
	lastRunTime, err := time.Parse(timeLayout, lastRunStr)
	if err != nil {
		return time.Now().Add(-firstRun)
	}
	return lastRunTime
}

// updateLastRunTime writes the current time as the last run time to a file.
func updateLastRunTime() {
	currentTime := time.Now().Format(timeLayout)
	err := ioutil.WriteFile(lastRunFile, []byte(currentTime), 0644)
	if err != nil {
		fmt.Println("Error updating last run time:", err)
	}
}

// runJournalctl executes the journalctl command and returns the output.
func runJournalctl(sinceTime time.Time) string {
	cmd := exec.Command("journalctl", "--no-pager", "-u", "faxq", "--since", sinceTime.Format(timeLayout))
	output, err := cmd.Output()
	if err != nil {
		fmt.Println("Error running journalctl:", err)
		return ""
	}
	return string(output)
}

// parseOutput processes the output to find specific lines and extract data.
func parseOutput(output string) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "NOTIFY: bin/notify") {
			qfile, why := extractInfo(line)

			log.Info("qfile: " + qfile + " why: " + why)

			if why == "rejected" ||
				why == "removed" ||
				why == "killed" {
			} else {
				continue
			}

			//qfile = strings.Replace(qfile, "\"", "", -1)
			//why = strings.Replace(why, "\"", "", -1)

			filePath := os.Getenv("BASE_HYLAFAX_PATH") + qfile

			log.Info("filePath: " + filePath)

			// Read the contents of the qfile
			qfileContents, err := readQfile(filePath)
			if err != nil {
				log.Errorf("Error reading qfile:", err)
			}

			qfileContents.Why = why

			// Send a webhook request
			err = sendWebhook(qfileContents)
			if err != nil {
				log.Error("Error sending webhook:", err)
			}

			log.Info("Webhook sent successfully")
		}
	}
	if err := scanner.Err(); err != nil {
		log.Error("Error scanning output:", err)
	}
}

// extractInfo extracts and prints the required information from the log line.
func extractInfo(line string) (string, string) {
	re := regexp.MustCompile(`NOTIFY: bin\/notify "([^"]*)" "([^"]*)"`)
	matches := re.FindStringSubmatch(line)
	if len(matches) >= 2 {
		//fmt.Printf("First Option: %s, Second Option: %s\n", matches[0], matches[1])
		return matches[1], matches[2]
	}

	return "", ""
}

type QFileData struct {
	SrcNum     string `json:"src_num"`
	SrcCid     string `json:"src_cid"`
	DestNum    string `json:"dest_num"`
	DestCid    string `json:"dest_cid"`
	Pages      int    `json:"total_pages"`
	TotalDials int    `json:"total_dials"`
	TotalTries int    `json:"total_tries"`
	JobID      int    `json:"job_id"`
	Status     string `json:"status"`
	Why        string `json:"why"`
}

func readQfile(filename string) (QFileData, error) {
	var data QFileData

	// Read the qfile contents
	qfile, err := OpenQfile(filename)
	if err != nil {
		return data, err
	}

	totPages, err := qfile.GetInt("totpages") // total pages
	if err != nil {
		return data, err
	}
	totTries, err := qfile.GetInt("tottries") // total tries
	if err != nil {
		return data, err
	}
	totDials, err := qfile.GetInt("totdials") // total dial attempts
	if err != nil {
		return data, err
	}
	jobID, err := qfile.GetInt("jobid") // total dial attempts
	if err != nil {
		return data, err
	}

	data = QFileData{
		SrcNum:     qfile.GetString("owner"),
		SrcCid:     qfile.GetString("tsi"),
		DestNum:    qfile.GetString("number"),
		DestCid:    qfile.GetString("external"),
		Pages:      totPages,
		TotalDials: totDials,
		TotalTries: totTries,
		Status:     qfile.GetString("status"),
		JobID:      jobID,
	}

	return data, nil

	/*
		tts:1706119431
		killtime:1706289964
		retrytime:0
		state:3
		npages:0
		totpages:1
		nskip:0
		skippages:0
		ncover:0
		coverpages:0
		ntries:0
		ndials:0
		totdials:8
		maxdials:50
		tottries:0
		maxtries:50
		pagewidth:215
		resolution:98
		pagelength:279
		priority:127
		schedpri:119
		minbr:0
		desiredbr:13
		desiredst:0
		desiredec:2
		desireddf:3
		desiredtl:0
		useccover:1
		usexvres:0
		external:12505652556
		number:12505652556
		mailaddr:root@faxrelay.topsoffice.ca
		sender:root
		jobid:1177
		jobtag:
		pagehandling:e0P
		modem:any
		faxnumber:
		tsi:2507632912
		receiver:
		company:
		location:
		voice:
		fromcompany:
		fromlocation:
		fromvoice:
		regarding:
		comments:
		cover:
		client:localhost
		owner:2507632912
		groupid:1177
		signalrate:14400
		dataformat:
		jobtype:facsimile
		tagline:
		subaddr:
		passwd:
		doneop:default
		commid:00003431
		csi:
		nsf:
		pagerange:
		status:The call dropped prematurely
		statuscode:0
		returned:0
		notify:none
		pagechop:default
		chopthreshold:3
		!tiff:0::docq/doc111.tif.1177
		fax:0::docq/doc111.tif;f0
	*/

}

func sendWebhook(data QFileData) error {
	// Prepare the webhook URL and credentials from environment variables
	webhookURL := os.Getenv("WEBHOOK_URL")
	username := os.Getenv("WEBHOOK_USERNAME")
	password := os.Getenv("WEBHOOK_PASSWORD")

	// Create an HTTP client with basic authentication
	client := &http.Client{}

	// Convert the data struct to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.SetBasicAuth(username, password)

	// Set JSON data in the request body
	req.Header.Set("Content-Type", "application/json")

	// Send the request
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Error(err)
		}
	}(resp.Body)

	// Check the response status
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("webhook request failed with status code: %d", resp.StatusCode)
	}

	return nil
}
