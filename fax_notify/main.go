package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/joho/godotenv"
	log "github.com/sirupsen/logrus"
	"io"
	"net/http"
	"os"
)

func main() {
	// Load environment variables from .env file
	err := godotenv.Load()
	if err != nil {
		fmt.Println("Error loading .env file")
		os.Exit(1)
	}

	if len(os.Args) < 3 {
		fmt.Println("Usage: notify <qfile> <why>")
		os.Exit(1)
	}

	qfile := os.Args[1]
	why := os.Args[2]

	filePath := os.Getenv("BASE_HYLAFAX_PATH") + qfile

	// Read the contents of the qfile
	qfileContents, err := readQfile(filePath)
	if err != nil {
		fmt.Println("Error reading qfile:", err)
		os.Exit(1)
	}

	qfileContents.Why = why

	// Send a webhook request
	err = sendWebhook(qfileContents)
	if err != nil {
		fmt.Println("Error sending webhook:", err)
		os.Exit(1)
	}

	fmt.Println("Webhook sent successfully!")
}

type QFileData struct {
	SrcNum     string `json:"src_num"`
	SrcCid     string `json:"src_cid"`
	DestNum    string `json:"dest_num"`
	DestCid    string `json:"dest_cid"`
	Pages      int    `json:"total_pages"`
	TotalDials int    `json:"total_dials"`
	TotalTries int    `json:"total_tries"`
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

	data = QFileData{
		SrcNum:     qfile.GetString("owner"),
		SrcCid:     qfile.GetString("tsi"),
		DestNum:    qfile.GetString("number"),
		DestCid:    qfile.GetString("external"),
		Pages:      totPages,
		TotalDials: totDials,
		TotalTries: totTries,
		Status:     qfile.GetString("status"),
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
