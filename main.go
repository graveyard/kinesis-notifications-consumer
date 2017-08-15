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
	"strings"
	"time"

	kbc "github.com/Clever/amazon-kinesis-client-go/batchconsumer"
	"github.com/Clever/amazon-kinesis-client-go/decode"
	"golang.org/x/time/rate"
	"gopkg.in/Clever/kayvee-go.v6/logger"
)

var lg = logger.New("kinesis-notifications-consumer")

// Slack is generally limited to 1 msg per second per hook
const MsgsPerSecond = 1

// Slack limits messages to 4k in length
const MaxMessageLength = 4000

type slackOutput struct {
	slackURL             string
	client               http.Client
	rateLimiter          *rate.Limiter
	rateLimitConcurrency int
	deployEnv            string
	minTimestamp         time.Time
	retryLimit           int
}

type slackTag struct {
	Channel  string `json:"channel"`
	Icon     string `json:"icon_emoji,omitempty"`
	Username string `json:"username,omitempty"`
}

type slackMessage struct {
	Text string `json:"text"`
	slackTag
}

func (s *slackOutput) oomKillerRoutes(fields map[string]interface{}) []decode.NotificationRoute {
	// All Production OOM killed messages go to slack
	content, ok := fields["rawlog"]
	if !ok || content == "" {
		// TODO: Should this be an error instead?
		return []decode.NotificationRoute{}
	}
	rawlog := content.(string)

	if !strings.Contains(rawlog, "oom-killer") {
		return []decode.NotificationRoute{}
	}
	program, ok := fields["programname"]
	if !ok || program != "kernel" {
		return []decode.NotificationRoute{}
	}

	env, ok := fields["env"]
	if !ok || env == "" {
		return []decode.NotificationRoute{}
	}

	hostname, ok := fields["hostname"]
	if !ok || hostname == "" {
		hostname = "unknown"
	}

	parts := strings.Split(rawlog, " ")
	killed := content
	if len(parts) > 1 {
		killed = parts[1]
	}

	return []decode.NotificationRoute{
		decode.NotificationRoute{
			Channel:  "#oncall-infra",
			Icon:     ":mindflayer:",
			Message:  fmt.Sprintf("<http://go/oom|OOM Killer invoked> %s (%s): %s", hostname, env, killed),
			User:     "Illithid",
			RuleName: "oom-killer",
		},
	}
}

func (s *slackOutput) notificationServiceRoutes(fields map[string]interface{}) []decode.NotificationRoute {
	// All Production Notification-Service messages go to slack, to be prcessed by the notorious-bot
	alertType, ok := fields["notification_alert_type"]
	if !ok || alertType == "" {
		return []decode.NotificationRoute{}
	}

	var alert struct {
		AlertType  string      `json:"notification_alert_type,omitempty"`
		AppID      string      `json:"app_id,omitempty"`
		DistrictID string      `json:"district_id,omitempty"`
		Value      string      `json:"value,omitempty"`
		Data       interface{} `json:"data,omitempty"`
	}

	rawMsg, err := json.Marshal(fields)
	if err != nil {
		return []decode.NotificationRoute{}
	}

	err = json.Unmarshal(rawMsg, &alert)
	if err != nil {
		return []decode.NotificationRoute{}
	}

	// Convert Data to a proper object
	if alert.Data != nil && alert.Data != "" {
		var data map[string]interface{}
		err = json.Unmarshal([]byte(alert.Data.(string)), &data)
		// If there's an error, just continue and skip this update
		if err == nil {
			alert.Data = data
		}
	}

	alertJson, err := json.Marshal(alert)
	if err != nil {
		return []decode.NotificationRoute{}
	}

	return []decode.NotificationRoute{
		decode.NotificationRoute{
			Channel:  "#notification-catcher",
			Icon:     ":notebook:",
			Message:  fmt.Sprintf("@notorious-bot: %s", alertJson),
			User:     "notice",
			RuleName: "notification-service",
		},
	}
}

func (s *slackOutput) globalRoutes(fields map[string]interface{}) []decode.NotificationRoute {
	routes := []decode.NotificationRoute{}

	// chain and append all global routes here
	routes = append(routes, s.oomKillerRoutes(fields)...)
	routes = append(routes, s.notificationServiceRoutes(fields)...)

	return routes
}

func (s *slackOutput) encodeMessage(fields map[string]interface{}) ([]byte, []string, error) {
	// Skip all non-notification messages
	kvmeta := decode.ExtractKVMeta(fields)
	routes := kvmeta.Routes.NotificationRoutes()
	routes = append(routes, s.globalRoutes(fields)...)
	if len(routes) <= 0 {
		return nil, nil, kbc.ErrMessageIgnored
	}

	// Assemble tags and message. This assumes that the only variability in the routes
	// are the channel, user, and icon
	tags := []string{}
	var text string
	for _, route := range routes {
		// Last message wins (they should all be the same)
		text = route.Message

		// Tag each message by channel, user, and icon
		encTag, err := json.Marshal(slackTag{
			Channel:  route.Channel,
			Username: route.User,
			Icon:     route.Icon,
		})
		if err != nil {
			return nil, nil, err
		}
		tags = append(tags, string(encTag))
	}

	// Make sure individual messages are not too long
	if len(text) > MaxMessageLength {
		return nil, nil, fmt.Errorf("Message length exceeds maximum length allowed by slack: %s", text)
	}

	return []byte(text), tags, nil
}

func (s *slackOutput) sendMessage(msg slackMessage) error {
	lastError := fmt.Errorf("Unknown error in output")

	// Encode the message to json
	msgStr, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("Failed to Marshall slack message %+v: %s", msg, err.Error())
	}

	for x := 0; x <= s.retryLimit; x++ {
		// Don't send too quickly
		s.rateLimiter.Wait(context.Background())

		// send the message to slack
		resp, err := s.client.Post(s.slackURL, "application/json", bytes.NewReader(msgStr))
		if err != nil {
			if err, ok := err.(net.Error); ok && err.Timeout() {
				// retry timeouts
				lastError = err
				continue
			}
			// Don't retry other errors
			return fmt.Errorf("Failed to post slack message: %+v, Error: %s", msg, err.Error())
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
			lg.InfoD("rate-limited-via-slack", logger.M{"delaying": delayTime})
			time.Sleep(time.Duration(delayTime) * time.Second)
			lastError = fmt.Errorf("Failed to post slack message: %+v. Error: Exceeded rate limit", msg)
		} else if resp.StatusCode >= 500 && resp.StatusCode < 600 {
			// A server side error occurred. Retry
			lg.ErrorD("slack-post-failed", logger.M{"status-code": resp.StatusCode, "retry": true})
			lastError = fmt.Errorf("Failed to post slack message: %+v. Error: Server returned '%d': %s", msg, resp.StatusCode, bodyString)
		} else if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			// Don't retry 400s
			lg.ErrorD("slack-post-failed", logger.M{"status-code": resp.StatusCode, "retry": false})
			return fmt.Errorf("Failed to post slack message: %+v. Error: Server returned '%d': %s", msg, resp.StatusCode, bodyString)
		}
	}

	// Retry attempts exceed
	return fmt.Errorf("Retry limit `%d` exceeded posting slack message: %+v. Error: %s", s.retryLimit, msg, lastError)
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
	// Decode the tag
	msgTag := slackTag{}
	err := json.Unmarshal([]byte(tag), &msgTag)
	if err != nil {
		return fmt.Errorf("Failed to decode message tag '%s': %s", tag, err.Error())
	}

	// Messages are batched by channel,user, & icon so just send one slack message
	// per batch with each message on a new line
	// limit total message length by size
	messages := []string{}
	for _, b := range batch {
		messages = append(messages, string(b))
	}
	message := slackMessage{
		Text:     strings.Join(messages, "\n"),
		slackTag: msgTag,
	}
	err = s.sendMessage(message)
	if err != nil {
		return kbc.PartialSendBatchError{
			FailedMessages: batch,
			ErrMessage:     err.Error(),
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
		BatchCount: 1,
		BatchSize:  MaxMessageLength,
	}
	output := newSlackOutput(env, slackURL, minTimestamp, ratelimitConcurrency, timeout, retryLimit)
	consumer := kbc.NewBatchConsumer(config, output)
	consumer.Start()
}
