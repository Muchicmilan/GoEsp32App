package main

import (
	"database/sql"
	"fmt"
	"log"
	_ "net/http"
	"os"
	"strconv"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
)

var db *sql.DB
var mqqtClient mqtt.Client

type Reading struct {
	Time        time.Time `json:"time"`
	Temperature float64   `json:"temperature"`
	Mac         string    `json:"mac"`
}

var messageHandler mqtt.MessageHandler = func(client mqtt.Client, msg mqtt.Message) {
	topic := msg.Topic()
	payload := string(msg.Payload())

	fmt.Printf("Received message: %s from topic: %s\n", payload, topic)
	parts := strings.Split(topic, "/")
	if len(parts) < 5 {
		return
	}
	mac := parts[4]
	tempStr := strings.Replace(payload, "C", "", 1)
	tempVal, err := strconv.ParseFloat(tempStr, 64)
	if err != nil {
		fmt.Printf("Error parsing temperature: %s\n", err)
		return
	}
	_, err = db.Exec("INSERT INTO readings (device_mac, temperature) VALUES ($1, $2)", mac, tempVal)
	if err != nil {
		fmt.Printf("Error inserting reading: %s\n", err)
	}
}

func getReadings(c *gin.Context) {
	rows, err := db.Query("SELECT device_mac, temperature, created_at FROM readings ORDER BY created_at DESC LIMIT 50")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var readings []Reading
	for rows.Next() {
		var r Reading
		rows.Scan(&r.Mac, &r.Temperature, &r.Time)
		readings = append(readings, r)
	}
	c.JSON(200, readings)
}

func setLed(c *gin.Context) {
	type Command struct {
		Mac   string `json:"mac"`
		State string `json:"state"`
	}
	var cmd Command
	if err := c.BindJSON(&cmd); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	topic := fmt.Sprintf("/sensor/to/set/led/%s", cmd.Mac)
	token := mqqtClient.Publish(topic, 1, false, cmd.State)
	token.Wait()

	c.JSON(200, gin.H{"status": "ok", "topic": topic, "val": cmd.State})
}

func main() {
	connStr := fmt.Sprintf("host=%s user=%s password=%s dbname=%s sslmode=disable",
		os.Getenv("DB_HOST"), os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD"), os.Getenv("DB_NAME"))
	var err error
	db, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		err = db.Ping()
		if err == nil {
			break
		}
		fmt.Printf("Baza nije spremna, pokušavam ponovo za 2 sekunde... (%d/10)\n", i+1)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Connected to database")
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS readings (
    id serial PRIMARY KEY,
    device_mac VARCHAR(20) NOT NULL,
    temperature FLOAT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
)`)
	if err != nil {
		log.Fatal("Error while creating readings table", err)
	}
	opts := mqtt.NewClientOptions()
	opts.AddBroker(os.Getenv("MQTT_BROKER"))
	opts.SetClientID(os.Getenv("MQTT_CLIENT_ID"))
	opts.SetUsername(os.Getenv("MQTT_USER"))
	opts.SetPassword(os.Getenv("MQTT_PASS"))

	opts.SetDefaultPublishHandler(messageHandler)

	mqttClient := mqtt.NewClient(opts)
	if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
		log.Fatal(token.Error())
	}
	fmt.Println("Connected to mqtt broker")
	mqttClient.Subscribe("/sensor/from/temp/+", 1, nil)
	r := gin.Default()

	r.StaticFile("/", "./index.html")
	r.GET("/api/readings", getReadings)
	r.POST("/api/set", setLed)
	err = r.Run(":8080")
	if err != nil {
		return
	}
}
