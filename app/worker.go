package main

import (
	"encoding/json"
	"fmt"
	"github.com/streadway/amqp"
	"log"
	"os"
	"strconv"
	"sync"
)

type Message struct {
	CampaignId string `json:"campaign_id"`
	QId        uint   `json:"questionary_id"`
}

var (
	rabbitHost string
	rabbitAddr string
	rabbitConn *amqp.Connection
	rabbitChan *amqp.Channel
	queueName  string
	numWorkers = 1
)

func init() {
	rabbitHost = os.Getenv("RABBIT_HOST")
	rPort := os.Getenv("RABBIT_PORT")
	rUser := os.Getenv("RABBIT_USER")
	rPassword := os.Getenv("RABBIT_PASSWORD")
	queueName = os.Getenv("RABBIT_QUEUE")
	if rabbitHost == "" || rUser == "" || rPassword == "" || queueName == "" {
		log.Fatal("[ERROR] RABBIT_HOST, RABBIT_USER, RABBIT_PASSWORD, RABBIT_QUEUE environment not specified")
	}
	if rPort == "" {
		rPort = "5672"
	}
	rabbitAddr = fmt.Sprintf("amqp://%s:%s@%s:%s/", rUser, rPassword, rabbitHost, rPort)

	nw := os.Getenv("WORKERS")
	if nw != "" {
		numWorkers, _ = strconv.Atoi(nw)
	}
}

func Consume() {
	var err error

	rabbitConn, err = amqp.Dial(rabbitAddr)

	if err != nil {
		log.Panic("[ERROR] Dial error " + err.Error())
	}

	rabbitChan, err = rabbitConn.Channel()
	if err != nil {
		log.Panic("[ERROR] open channel error " + err.Error())
	}
	defer rabbitChan.Close()

	q, err := rabbitChan.QueueDeclare(
		queueName, // name
		true,      // durable
		false,     // delete when unused
		false,     // exclusive
		false,     // no-wait
		nil,       // arguments
	)
	if err != nil {
		log.Panic("[ERROR] QueueDeclare error " + err.Error())
	}

	err = rabbitChan.Qos(
		1,     // prefetch count
		0,     // prefetch size
		false, // global
	)
	if err != nil {
		log.Panic("[ERROR] set Qos error " + err.Error())
	}

	tasks, err := rabbitChan.Consume(
		q.Name, // queue
		"",     // consumer
		false,  // auto-ack
		false,  // exclusive
		false,  // no-local
		false,  // no-wait
		nil,    // args
	)
	if err != nil {
		log.Panic("[ERROR] register consumer error " + err.Error())
	}

	log.Printf("[INFO] Running consumer host=%s queue=%s workers=%d", rabbitHost, queueName, numWorkers)
	wg := &sync.WaitGroup{}
	wg.Add(1)

	for i := 0; i <= numWorkers; i++ {
		go worker(tasks)
	}
	wg.Wait()
}

func worker(tasks <-chan amqp.Delivery) {
	for taskItem := range tasks {
		task := &Message{}
		err := json.Unmarshal(taskItem.Body, task)
		if err != nil {
			fmt.Println("[ERROR] cant unpack json", err)
			taskItem.Ack(false)
			continue
		}
		fmt.Printf("[DEBUG] incoming task %+v\n", task)

		taskItem.Ack(false)
	}
}
