package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	kbc "github.com/Clever/amazon-kinesis-client-go/batchconsumer"
	"github.com/Clever/kinesis-to-firehose/decode"
	"golang.org/x/time/rate"
)

// Slack is generally limited to 1 msg per second per hook
const MsgsPerSecond = 1

type slackOutput struct {
	slackURL             string
	client               http.Client
	rateLimiter          *rate.Limiter
	rateLimitConcurrency int
	deployEnv            string
	minTimestamp         time.Time
	retryLimit           int
}

type slackMessage struct {
	Channel  string `json:"channel"`
	Text     string `json:"text"`
	Icon     string `json:"icon_emoji,omitempty"`
	Username string `json:"username,omitempty"`
}

func (s *slackOutput) fetchField(fields map[string]interface{}, key string) (string, error) {
	rawlog, ok := fields["rawlog"]
	if !ok {
		return "", fmt.Errorf("`rawlog` missing in notification: '%+v'", fields)
	}
	value, ok := fields[key]
	if !ok {
		return "", fmt.Errorf("`%s` field not found in notification: '%s'", key, rawlog.(string))
	}
	return value.(string), nil
}

func (s *slackOutput) fetchRequiredField(fields map[string]interface{}, key string) (string, error) {
	value, err := s.fetchField(fields, key)
	if err != nil {
		return "", err
	}

	if value == "" {
		return "", fmt.Errorf("`%s` field is empty in notification: '%s'", key, fields["rawlog"])
	}
	return value, nil
}

func (s *slackOutput) encodeMessage(fields map[string]interface{}) ([]byte, []string, error) {
	// Skip all non-notification messages
	msgType, err := s.fetchField(fields, "_kvmeta.type")
	if err != nil || msgType != "notifications" {
		return nil, []string{}, kbc.ErrMessageIgnored
	}

	// Get all of the fields used in a slack message
	channel, err := s.fetchRequiredField(fields, "_kvmeta.channel")
	if err != nil {
		return nil, []string{}, err
	}

	text, err := s.fetchRequiredField(fields, "_kvmeta.message")
	if err != nil {
		return nil, []string{}, err
	}

	icon, err := s.fetchField(fields, "_kvmeta.icon")
	if err != nil {
		return nil, []string{}, err
	}

	user, err := s.fetchField(fields, "_kvmeta.user")
	if err != nil {
		return nil, []string{}, err
	}

	// Package up the slack message
	msg := slackMessage{
		Channel:  channel,
		Text:     text,
		Icon:     icon,
		Username: user,
	}

	slackMsg, err := json.Marshal(msg)
	if err != nil {
		return nil, []string{}, err
	}

	return slackMsg, []string{channel}, nil
}

func (s *slackOutput) sendMessage(msg []byte, tag string) error {
	lastError := fmt.Errorf("Unknown error in output")
	for x := 0; x <= s.retryLimit; x++ {
		// Don't send too quickly
		s.rateLimiter.Wait(context.TODO())

		// send the message to slack
		resp, err := s.client.Post(s.slackURL, "application/json", bytes.NewReader(msg))
		if err != nil {
			if err, ok := err.(net.Error); ok && err.Timeout() {
				// retry timeouts
				lastError = err
				continue
			}
			// Don't retry other errors
			return fmt.Errorf("Failed to post to `%s`. Message: %s, Error: %s", tag, string(msg), err.Error())
		}
		defer resp.Body.Close()

		// Attempt to get the response body to aid debugging
		bodyString := ""
		if resp.Body != nil {
			bodyBytes, err := ioutil.ReadAll(resp.Body)
			if err == nil {
				bodyString = string(bodyBytes)
			}
		}

		if resp.StatusCode == 200 {
			// Done!
			return nil
		} else if resp.StatusCode == 429 {
			// Rate limited. Retry after `Retry-After` seconds in the header
			delayTime := rand.Intn(s.rateLimitConcurrency)
			retry := resp.Header.Get("Retry-After")
			if retry != "" {
				sec, err := strconv.Atoi(retry)
				if err != nil {
					// Without a 'Retry-After' this will delay by the number of consumers
					delayTime = delayTime + s.rateLimitConcurrency
				} else {
					delayTime = delayTime + sec
				}
			}
			time.Sleep(time.Duration(delayTime) * time.Second)
			lastError = fmt.Errorf("Failed to post to `%s`. Message: `%s`. Error: Exceeded rate limit", tag, string(msg))
		} else if resp.StatusCode >= 500 && resp.StatusCode < 600 {
			// A server side error occurred. Retry
			lastError = fmt.Errorf("Failed to post to `%s`. Message: `%s`. Error: Server returned '%d': %s", tag, string(msg), resp.StatusCode, bodyString)
		} else if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			// Don't retry 400s
			return fmt.Errorf("Failed to post to `%s`. Message: `%s`. Error: Server returned '%d': %s", tag, string(msg), resp.StatusCode, bodyString)
		}
	}

	// Retry attempts exceed
	return fmt.Errorf("Retry limit `%d` exceeded posting to `%s`. Message: `%s`. Error: %s", s.retryLimit, tag, string(msg), lastError)
}

// ProcessMessage is called once per log to parse the log line and then reformat it
// so that it can be directly used by the output. The returned tags will be passed along
// with the encoded log to SendBatch()
func (s *slackOutput) ProcessMessage(rawmsg []byte) (msg []byte, tags []string, err error) {
	// Parse the log line
	fields, err := decode.ParseAndEnhance(string(rawmsg), s.deployEnv, false, false, s.minTimestamp)
	if err != nil {
		return nil, []string{}, err
	}

	return s.encodeMessage(fields)
}

// SendBatch is called once per batch per tag and delivers the message to slack
func (s *slackOutput) SendBatch(batch [][]byte, tag string) error {
	failedMessages := [][]byte{}
	var lastError error

	for _, msg := range batch {
		err := s.sendMessage(msg, tag)
		if err != nil {
			failedMessages = append(failedMessages, msg)
			lastError = err
		}
	}

	if len(failedMessages) > 0 {
		return kbc.PartialSendBatchError{
			FailedMessages: failedMessages,
			ErrMessage:     lastError.Error(),
		}
	}
	return nil
}

func newSlackOutput(env, slackURL string, minTimestamp, ratelimitConcurrency, timeout, retryLimit int) *slackOutput {
	parsedURL, err := url.Parse(slackURL)
	if err != nil {
		log.Panicf("Malformed slack url: `%s`: %s\n", slackURL, err.Error())
	}

	if parsedURL.Scheme != "https" && parsedURL.Scheme != "http" {
		log.Panicf("Slack URL must be either http or https\n")
	}

	// Slack rate limits by hook, but this consumer will run once per shard
	// so collectively the consumers the consumers need to be rate limited at
	// 1 msg per second. The RATELIMIT_CONCURRENCY env var is used to describe
	// the number of consumers to factor in when rate limiting each one.
	rateLimit := MsgsPerSecond / ratelimitConcurrency

	// If the consumers still exceed the rate limit, each one will back off
	// for the minimum required time + a small randomized time based on
	// RATELIMIT_CONCURRENCY
	rand.Seed(time.Now().Unix())

	return &slackOutput{
		slackURL: slackURL,
		client: http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
		rateLimiter:          rate.NewLimiter(rate.Limit(rateLimit), MsgsPerSecond),
		rateLimitConcurrency: ratelimitConcurrency,
		deployEnv:            env,
		minTimestamp:         time.Unix(int64(minTimestamp), 0),
		retryLimit:           retryLimit,
	}
}

// getEnv returns required environment variable
func getEnv(key string) string {
	value := os.Getenv(key)
	if value == "" {
		log.Panicf("Missing env var: %s\n", key)
	}
	return value
}

// getIntEnv returns the required environment variable as an int
func getIntEnv(key string) int {
	value, err := strconv.Atoi(getEnv(key))
	if err != nil {
		log.Panicf("Environment variable `%s` is not an integer: %s", key, err.Error())
	}
	return value
}

func main() {
	env := getEnv("DEPLOY_ENV")
	minTimestamp := getIntEnv("MINIMUM_TIMESTAMP")
	ratelimitConcurrency := getIntEnv("RATELIMIT_CONCURRENCY")
	timeout := getIntEnv("SLACK_TIMEOUT")
	retryLimit := getIntEnv("SLACK_RETRY_LIMIT")
	slackURL := getEnv("SLACK_HOOK_URL")

	// Slack doesn't support batching, so set the batch size to 1
	config := kbc.Config{
		LogFile:    "/tmp/kinesis-notifications-consumer-" + time.Now().Format(time.RFC3339),
		DeployEnv:  env,
		BatchCount: 1,
	}
	output := newSlackOutput(env, slackURL, minTimestamp, ratelimitConcurrency, timeout, retryLimit)
	consumer := kbc.NewBatchConsumer(config, output)
	consumer.Start()
}
