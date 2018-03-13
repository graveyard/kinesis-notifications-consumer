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
	"path"
	"strconv"
	"strings"
	"time"

	kbc "github.com/Clever/amazon-kinesis-client-go/batchconsumer"
	"github.com/Clever/amazon-kinesis-client-go/decode"
	"github.com/kardianos/osext"
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
	retryLimit           int
	throttleThreshold    int

	channelThottles map[string]channelStats
	unknownChannels map[string]time.Time
}

type slackMessage struct {
	Parse    string `json:"parse,omitempty"`
	Channel  string `json:"channel"`
	Icon     string `json:"icon_emoji,omitempty"`
	Username string `json:"username,omitempty"`
	Text     string `json:"text"`
}

type channelStats struct {
	lastSent time.Time
	msgCount int
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
		Value      interface{} `json:"value,omitempty"`
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

	if alert.Value != nil {
		// Convert value into a string version, if it's not, for consistency.
		alert.Value = fmt.Sprintf("%v", alert.Value)
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
	messages := []slackMessage{}
	for _, route := range routes {
		// Make sure individual messages are not too long
		if len(route.Message) > MaxMessageLength {
			return nil, nil, fmt.Errorf("Exceeds max-length allowed by slack: %s", route.Message)
		}

		messages = append(messages, slackMessage{
			Channel:  route.Channel,
			Username: route.User,
			Icon:     route.Icon,
			Text:     route.Message,
		})
	}

	encodedMsgs, err := json.Marshal(messages)
	if err != nil {
		return nil, nil, err
	}

	return encodedMsgs, []string{"all"}, nil
}

func (s *slackOutput) updateThrottle(channel string) (bool, bool) {
	stats := s.channelThottles[channel]
	stats = channelStats{time.Now(), stats.msgCount + 1}
	s.channelThottles[channel] = stats

	return stats.msgCount > s.throttleThreshold, stats.msgCount == s.throttleThreshold
}

func (s *slackOutput) reapTrackedChannels() {
	toDelete := []string{}
	for channel, ts := range s.unknownChannels {
		if time.Since(ts) > time.Hour {
			toDelete = append(toDelete, channel)
		}
	}
	for _, channel := range toDelete {
		delete(s.unknownChannels, channel)
	}

	toDelete = []string{}
	for channel, stats := range s.channelThottles {
		if time.Since(stats.lastSent) > 10*time.Minute {
			toDelete = append(toDelete, channel)
		} else if time.Since(stats.lastSent) > time.Minute {
			if stats.msgCount > 10 {
				stats.msgCount = 11
			}
			s.channelThottles[channel] = channelStats{time.Now(), stats.msgCount - 1}
		}
	}
	for _, channel := range toDelete {
		delete(s.channelThottles, channel)
	}
}

func (s *slackOutput) sendMessage(msg slackMessage) error {
	s.reapTrackedChannels()

	if _, ok := s.unknownChannels[msg.Channel]; ok {
		return fmt.Errorf("Attempted to send message to unknown channel")
	}

	isThrottled, isNearlyThrottled := s.updateThrottle(msg.Channel)
	if isThrottled {
		return fmt.Errorf("Message to channel throttled")
	}
	if isNearlyThrottled { // TODO: maybe link to documentation or #oncall-infra
		msg.Text += "\nMessages to this channel are being throttled :sixgod:"
	}

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
			delayTime := rand.Intn(s.rateLimitConcurrency) // adding fuzz
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
		} else if resp.StatusCode == 404 {
			if _, ok := s.unknownChannels[msg.Channel]; !ok {
				s.unknownChannels[msg.Channel] = time.Now()
				_ = s.sendMessage(slackMessage{
					Text:     fmt.Sprintf("Unknown channel: %s\n> %s", msg.Channel, msg.Text),
					Channel:  "#oncall-infra",
					Icon:     ":orly:",
					Username: "kinesis-notification-consumer",
				})
			}

			lg.ErrorD("unknown-channel", logger.M{
				"status-code": resp.StatusCode, "retry": false, "channel": msg.Channel,
			})
			return fmt.Errorf("404 from slack channel: %s", msg.Channel)
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

var _kvmeta = []byte("_kvmeta")
var _notifications = []byte("notifications")
var _oomkill = []byte("oom-killer")
var _notification_alert_type = []byte("notification_alert_type")

func (s *slackOutput) hasNotifications(rawmsg []byte) bool {
	return (bytes.Contains(rawmsg, _kvmeta) && bytes.Contains(rawmsg, _notifications)) ||
		bytes.Contains(rawmsg, _oomkill) || bytes.Contains(rawmsg, _notification_alert_type)
}

// ProcessMessage is called once per log to parse the log line and then reformat it
// so that it can be directly used by the output. The returned tags will be passed along
// with the encoded log to SendBatch()
func (s *slackOutput) ProcessMessage(rawmsg []byte) (msg []byte, tags []string, err error) {
	if !s.hasNotifications(rawmsg) {
		return nil, nil, kbc.ErrMessageIgnored
	}

	// Parse the log line
	fields, err := decode.ParseAndEnhance(string(rawmsg), s.deployEnv)
	if err != nil {
		return nil, []string{}, err
	}

	return s.encodeMessage(fields)
}

// SendBatch is called once per batch per tag and delivers the message to slack
func (s *slackOutput) SendBatch(batch [][]byte, tag string) error {
	messages := []slackMessage{}
	for _, b := range batch {
		var msgs []slackMessage
		err := json.Unmarshal(b, &msgs)
		if err != nil {
			return fmt.Errorf("Failed to decode batch message '%s': %s", b, err.Error())
		}

		messages = append(messages, msgs...)
	}

	for _, msg := range messages {
		// Notification-service alerts should be passed in raw, without slack doing link/ID parsing.
		if msg.Username == "notice" && msg.Channel == "#notification-catcher" {
			msg.Parse = "none"
		}

		err := s.sendMessage(msg)
		if err != nil {
			return kbc.PartialSendBatchError{FailedMessages: batch, ErrMessage: err.Error()}
		}
	}

	return nil
}

func newSlackOutput(env, slackURL string, ratelimitConcurrency, timeout int) *slackOutput {
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
	rateLimit := float64(MsgsPerSecond) / float64(ratelimitConcurrency)

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
		retryLimit:           5,
		throttleThreshold:    5,

		channelThottles: map[string]channelStats{},
		unknownChannels: map[string]time.Time{},
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

func setupLogRouting() {
	dir, err := osext.ExecutableFolder()
	if err != nil {
		log.Fatal(err)
	}
	err = logger.SetGlobalRouting(path.Join(dir, "kvconfig.yml"))
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	env := getEnv("DEPLOY_ENV")
	ratelimitConcurrency := getIntEnv("RATELIMIT_CONCURRENCY")
	timeout := getIntEnv("SLACK_TIMEOUT")
	retryLimit := getIntEnv("SLACK_RETRY_LIMIT")
	slackURL := getEnv("SLACK_HOOK_URL")
	throttleThreshold := getIntEnv("THROTTLE_THRESHOLD")

	setupLogRouting()

	// Slack doesn't support batching, so set the batch size to 1
	config := kbc.Config{
		FailedLogsFile: "/tmp/kinesis-notifications-consumer-" + time.Now().Format(time.RFC3339),
		BatchCount:     1,
		BatchSize:      MaxMessageLength,
		ReadRateLimit:  getIntEnv("READ_RATE_LIMIT"),
	}

	output := newSlackOutput(env, slackURL, ratelimitConcurrency, timeout)
	output.retryLimit = retryLimit
	output.throttleThreshold = throttleThreshold

	consumer := kbc.NewBatchConsumer(config, output)
	consumer.Start()
}
