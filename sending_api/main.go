package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/basicauth"
)

type XFRecord struct {
	Ts       time.Time `json:"ts"`
	Filename string    `json:"filename"`
	Destnum  string    `json:"destnum"`
	Cidname  string    `json:"cidname"`
	Cidnum   string    `json:"cidnum"`
}

func main() {
	// Load environment variables
	spoolDir := os.Getenv("SPOOL_DIR")
	if spoolDir == "" {
		spoolDir = "./spool"
	}
	username := os.Getenv("BASIC_AUTH_USER")
	if username == "" {
		username = "admin"
	}
	password := os.Getenv("BASIC_AUTH_PASS")
	if password == "" {
		password = "password123"
	}
	retryCount := os.Getenv("FAX_RETRY_COUNT")
	if retryCount == "" {
		retryCount = "3"
	}

	// Create the spool directory if it doesn't exist
	if _, err := os.Stat(spoolDir); os.IsNotExist(err) {
		if err := os.Mkdir(spoolDir, 0755); err != nil {
			log.Fatalf("Failed to create spool directory: %v", err)
		}
	}

	app := fiber.New()

	// Basic Auth middleware
	app.Use(basicauth.New(basicauth.Config{
		Users: map[string]string{
			username: password,
		},
	}))

	// Endpoint to handle fax upload and processing
	app.Put("/upload", func(c *fiber.Ctx) error {
		// Extract query parameters
		destination := c.Query("destination")
		source := c.Query("source")
		callerIDName := c.Query("cidname", "Unknown")
		callerIDNumber := c.Query("cidnum", source)

		if destination == "" || source == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "destination and source query parameters are required",
			})
		}

		// Retrieve the file from the request
		fileHeader, err := c.FormFile("file")
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "failed to retrieve file from request",
			})
		}

		// Save the file to the spool directory
		filename := c.Query("filename", fmt.Sprintf("fax_%d.pdf", time.Now().Unix()))
		filePath := filepath.Join(spoolDir, filename)
		err = c.SaveFile(fileHeader, filePath)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to save the uploaded file",
			})
		}

		// Prepare the XFRecord
		entry := XFRecord{
			Ts:       time.Now(),
			Filename: filename,
			Destnum:  destination,
			Cidname:  callerIDName,
			Cidnum:   callerIDNumber,
		}

		// Call sendFax in a goroutine to process asynchronously
		go func() {
			if err := sendFax(entry, spoolDir, retryCount); err != nil {
				log.Printf("Error sending fax: %v", err)
			}
		}()

		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"message":     "fax file received",
			"destination": destination,
			"source":      source,
			"path":        filePath,
		})
	})

	// Start the server
	log.Println("Server running on http://localhost:3000")
	log.Fatal(app.Listen(":3000"))
}

// sendFax processes the XFRecord and sends the fax using the `sendfax` command
func sendFax(entry XFRecord, spoolDir string, retryCount string) error {
	time.Sleep(2 * time.Second) // Simulate waiting for the fax file to be ready

	log.Printf("Sending fax: %s to %s", entry.Filename, entry.Destnum)

	// Construct the sendfax command
	cmd := exec.Command("/bin/bash", "-c", "sendfax"+
		" -n -S "+entry.Cidnum+
		" -o "+entry.Cidnum+
		" -c \""+entry.Cidname+
		"\" -k \"now + 2 days\""+
		" -T "+retryCount+
		" -t "+retryCount+
		" -d "+entry.Destnum+
		" "+filepath.Join(spoolDir, entry.Filename))

	// Execute the command
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("sendfax command failed: %v", err)
		return fmt.Errorf("sendfax command failed: %w", err)
	}
	log.Printf("sendfax output: %s", string(output))

	// Delete the fax file after sending
	err = os.Remove(filepath.Join(spoolDir, entry.Filename))
	if err != nil {
		log.Printf("Failed to delete fax file: %v", err)
		return err
	}

	log.Printf("Fax file %s deleted successfully", entry.Filename)
	return nil
}
