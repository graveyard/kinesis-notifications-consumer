package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
)

const mockURL = "https://mockery.com/call/me"

// TestOOmKillerRoutes tests that the globalRoutes() helper used in encodeMessage() handles all global slack routes
func TestOomKillerRoutes(t *testing.T) {
	sender := newSlackOutput("test", mockURL, 1, 1)
	sender.retryLimit = 3

	t.Log("Nominal Case ( a production oom-killer log)")
	log := "[14214865.119571] myapp invoked oom-killer: gfp_mask=0x24000c0, order=0, oom_score_adj=0"
	input := map[string]interface{}{
		"rawlog":      log,
		"_kvmeta":     map[string]interface{}{},
		"env":         "production",
		"hostname":    "ip-1-0-1-0",
		"programname": "kernel",
	}

	routes := sender.globalRoutes(input)
	assert.Equal(t, 1, len(routes))
	assert.Contains(t, routes[0].Message, "ip-1-0-1-0")
	assert.Contains(t, routes[0].Message, "production")
	assert.Contains(t, routes[0].Message, "myapp")

	t.Log("Non kernel")
	input = map[string]interface{}{
		"rawlog":      log,
		"_kvmeta":     map[string]interface{}{},
		"env":         "dev",
		"hostname":    "ip-1-0-1-0",
		"programname": "other-app",
	}

	routes = sender.oomKillerRoutes(input)
	assert.Equal(t, 0, len(routes))

	t.Log("Non oom-killer")
	log = "Hello World"
	input = map[string]interface{}{
		"rawlog":      log,
		"_kvmeta":     map[string]interface{}{},
		"env":         "dev",
		"hostname":    "ip-1-0-1-0",
		"programname": "kernel",
	}

	routes = sender.oomKillerRoutes(input)
	assert.Equal(t, 0, len(routes))
}

// TestNotificationServiceRoutes tests that the globalRoutes() helper used in encodeMessage() handles all global slack routes
func TestNotificationServiceRoutes(t *testing.T) {
	sender := newSlackOutput("test", mockURL, 1, 1)
	sender.retryLimit = 3

	t.Log("Complete Case (all data fields exist)")
	input := map[string]interface{}{
		"env": "production",
		"notification_alert_type": "test_alert",
		"app_id":                  "app__id",
		"district_id":             "district__id",
		"value":                   "314159",
		"data":                    map[string]interface{}{"some": "data", "with": "meaning"},
	}

	routes := sender.globalRoutes(input)
	assert.Equal(t, 1, len(routes))
	assert.Equal(t, routes[0].Message, `@notorious-bot: {"notification_alert_type":"test_alert","app_id":"app__id","district_id":"district__id","value":"314159","data":{"some":"data","with":"meaning"}}`)

	t.Log("Simple Case (just an alert type, no other data)")
	input = map[string]interface{}{
		"env": "production",
		"notification_alert_type": "test_alert",
	}

	routes = sender.globalRoutes(input)
	assert.Equal(t, 1, len(routes))
	assert.Equal(t, routes[0].Message, `@notorious-bot: {"notification_alert_type":"test_alert"}`)

	t.Log("Works OK if data is just a string.")
	input = map[string]interface{}{
		"env": "production",
		"notification_alert_type": "test_alert",
		"data": "foobar",
	}

	routes = sender.globalRoutes(input)
	assert.Equal(t, 1, len(routes))
	assert.Equal(t, routes[0].Message, `@notorious-bot: {"notification_alert_type":"test_alert","data":"foobar"}`)

	t.Log("Works OK if value is an int instead of a string.")
	input = map[string]interface{}{
		"env": "production",
		"notification_alert_type": "test_alert",
		"value":                   3,
	}

	routes = sender.globalRoutes(input)
	assert.Equal(t, 1, len(routes))
	assert.Equal(t, routes[0].Message, `@notorious-bot: {"notification_alert_type":"test_alert","value":"3"}`)

	t.Log("Works OK if value is a boolean instead of a string.")
	input = map[string]interface{}{
		"env": "production",
		"notification_alert_type": "test_alert",
		"value":                   true,
	}

	routes = sender.globalRoutes(input)
	assert.Equal(t, 1, len(routes))
	assert.Equal(t, routes[0].Message, `@notorious-bot: {"notification_alert_type":"test_alert","value":"true"}`)

	t.Log("Still works OK if value is a boolean hidden inside of a string.")
	input = map[string]interface{}{
		"env": "production",
		"notification_alert_type": "test_alert",
		"value":                   "true",
	}

	routes = sender.globalRoutes(input)
	assert.Equal(t, 1, len(routes))
	assert.Equal(t, routes[0].Message, `@notorious-bot: {"notification_alert_type":"test_alert","value":"true"}`)

	t.Log("Non notification-service")
	input = map[string]interface{}{
		"env":     "production",
		"message": "hello, world",
	}

	routes = sender.oomKillerRoutes(input)
	assert.Equal(t, 0, len(routes))

	t.Log("Non production")
	input = map[string]interface{}{
		"notification_alert_type": "test_alert",
		"env": "dev",
	}

	routes = sender.oomKillerRoutes(input)
	assert.Equal(t, 0, len(routes))
}

func TestHasNotifications(t *testing.T) {
	assert := assert.New(t)

	sender := newSlackOutput("test", mockURL, 1, 1)
	sender.retryLimit = 3

	tests := []struct {
		message          string
		hasNotifications bool
	}{
		{
			message:          "non-kayvee logs are matched",
			hasNotifications: false,
		},
		{
			message: "[14214865.119571] myapp invoked " +
				"oom-killer: gfp_mask=0x24000c0, order=0, oom_score_adj=0",
			hasNotifications: true,
		},
		{
			message: "@notorious-bot: " +
				`{"notification_alert_type":"test_alert","data":"foobar"}`,
			hasNotifications: true,
		},
		{
			message: "@notorious-bot: " +
				`{"notification_alert_type":"test_alert","data":"foobar"}`,
			hasNotifications: true,
		},
		{
			message: `{"_kvmeta":{routes":[{"channel":"#deploys","icon":":catapult:","message":` +
				`"hi","rule":"prod-app-scaling","type":"notifications","user":"catapult"}],` +
				`"team":"eng-infra"}}`,
			hasNotifications: true,
		},
		{
			message: `{"_kvmeta":{routes":[{"series":"not-a-notification","type":"analytics"}],` +
				`"team":"eng-infra"}}`,
			hasNotifications: false,
		},
		{
			message: `{"_kvmeta":{routes":[{"channel":"#deploys","icon":":catapult:","message":` +
				`"hi","rule":"prod-app-scaling","type":"notifications","user":"catapult"},` +
				`{"channel":"#multi-notify-message","icon":":catapult:","message":"hi",` +
				`"rule":"prod-app-scaling","type":"notifications","user":"catapult"}],` +
				`"team":"eng-infra"}}`,
			hasNotifications: true,
		},
	}

	for _, test := range tests {
		t.Logf("hasNotifications? [%t] %s", test.hasNotifications, test.message)

		assert.Equal(test.hasNotifications, sender.hasNotifications([]byte(test.message)))
	}

}

// TestEncodeMessage tests the encodeMessage() helper used in ProcessMessage()
func TestEncodeMessage(t *testing.T) {
	sender := newSlackOutput("test", mockURL, 1, 1)
	sender.retryLimit = 3

	t.Log("Nominal case")
	log := "slack message goes here"
	input := map[string]interface{}{
		"rawlog": log,
		"_kvmeta": map[string]interface{}{
			"routes": []interface{}{
				map[string]interface{}{
					"type":    "notifications",
					"channel": "#test",
					"message": "Hello World",
					"user":    "testbot",
					"icon":    ":bot:",
				},
			},
		},
	}

	output, tags, err := sender.encodeMessage(input)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(tags))
	assert.Equal(t, "all", tags[0])

	expectedMsg := slackMessage{
		Channel:  "#test",
		Text:     "Hello World",
		Username: "testbot",
		Icon:     ":bot:",
	}
	var outMsg []slackMessage
	_ = json.Unmarshal(output, &outMsg)
	assert.EqualValues(t, 1, len(outMsg))
	assert.EqualValues(t, expectedMsg, outMsg[0])

	t.Log("Multiple routes")
	input = map[string]interface{}{
		"rawlog": log,
		"_kvmeta": map[string]interface{}{
			"routes": []interface{}{
				map[string]interface{}{
					"type":    "notifications",
					"channel": "#test",
					"message": "Hello World1",
					"user":    "testbot",
					"icon":    ":bot:",
				},
				map[string]interface{}{
					"type":    "notifications",
					"channel": "#test2",
					"message": "Hello World2",
					"user":    "testbot2",
					"icon":    ":bot2:",
				},
			},
		},
	}

	output, tags, err = sender.encodeMessage(input)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(tags))
	assert.Equal(t, "all", tags[0])

	var outMsgs []slackMessage
	_ = json.Unmarshal(output, &outMsgs)
	assert.EqualValues(t, 2, len(outMsgs))

	expectedMsgs := []slackMessage{
		slackMessage{
			Channel:  "#test",
			Text:     "Hello World1",
			Username: "testbot",
			Icon:     ":bot:",
		},
		slackMessage{
			Channel:  "#test2",
			Text:     "Hello World2",
			Username: "testbot2",
			Icon:     ":bot2:",
		},
	}
	assert.EqualValues(t, expectedMsgs, outMsgs)

	t.Log("Missing the raw log")
	input = map[string]interface{}{
		"_kvmeta.type":    "notifications",
		"_kvmeta.channel": "#test",
		"_kvmeta.message": "Hello World",
		"_kvmeta.user":    "testbot",
		"_kvmeta.icon":    ":bot:",
	}
	output, tags, err = sender.encodeMessage(input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "intentionally skipped")

	t.Log("Not a notification")
	input = map[string]interface{}{
		"rawlog": log,
		"_kvmeta": map[string]interface{}{
			"routes": []interface{}{
				map[string]interface{}{
					"type":    "metric",
					"channel": "#test",
					"message": "Hello World",
					"user":    "testbot",
					"icon":    ":bot:",
				},
			},
		},
	}
	output, tags, err = sender.encodeMessage(input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "intentionally skipped")
}

// TestEncodeMessageMaxSize tests that encodeMessage() discards messages that
// are too long
func TestEncodeMessageMaxSize(t *testing.T) {
	sender := newSlackOutput("test", mockURL, 1, 1)
	sender.retryLimit = 3

	t.Log("Nominal case")
	log := "slack message goes here"
	input := map[string]interface{}{
		"rawlog": log,
		"_kvmeta": map[string]interface{}{
			"routes": []interface{}{
				map[string]interface{}{
					"type":    "notifications",
					"channel": "#test",
					"message": strings.Repeat("#", MaxMessageLength+1),
					"user":    "testbot",
					"icon":    ":bot:",
				},
			},
		},
	}

	_, _, err := sender.encodeMessage(input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Exceeds max-length allowed by slack")
}

// TestSendBatch tests the nominal expected behavior of SendBatch()
func TestSendBatch(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	t.Log("a fake batch of slack messages")
	msgs := []slackMessage{
		slackMessage{
			Channel:  "#channelA",
			Icon:     ":fire:",
			Username: "fire",
			Text:     "All your schools are belong to us",
		},
		slackMessage{
			Channel:  "#channelB",
			Icon:     ":fire:",
			Username: ":fire:",
			Text:     "All your apps are belong to us",
		},
		slackMessage{
			Channel:  "#notification-catcher",
			Icon:     ":fire:",
			Username: ":fire:",
			Text:     "All your monies are belong to us",
		},
	}
	msgBatches := [][]slackMessage{
		[]slackMessage{msgs[0], msgs[1]},
		[]slackMessage{msgs[2]},
	}

	batch := [][]byte{}
	for _, msg := range msgBatches {
		enc, err := json.Marshal(msg)
		assert.NoError(t, err)

		batch = append(batch, enc)
	}

	messagesReceived := 0
	httpmock.RegisterResponder("POST", mockURL,
		func(req *http.Request) (*http.Response, error) {
			t.Log("Verify expected headers")
			assert.Equal(t, "application/json", req.Header["Content-Type"][0])

			t.Log("Verify expected JSON body")
			decoder := json.NewDecoder(req.Body)
			var msg slackMessage
			err := decoder.Decode(&msg)
			assert.NoError(t, err)

			oriMsg := msgs[messagesReceived]
			assert.Equal(t, oriMsg.Text, msg.Text)
			assert.Equal(t, oriMsg.Channel, msg.Channel)
			assert.Equal(t, oriMsg.Icon, msg.Icon)
			assert.Equal(t, oriMsg.Username, msg.Username)
			assert.Equal(t, "", msg.Parse)

			resp, err := httpmock.NewJsonResponse(200, nil)
			if err != nil {
				panic(err) // failure in test mocks
			}
			messagesReceived++
			return resp, nil
		},
	)

	sender := newSlackOutput("test", mockURL, 1, 1)
	sender.retryLimit = 3
	err := sender.SendBatch(batch, "all")
	assert.NoError(t, err)
	assert.Equal(t, 3, messagesReceived)
}

// TestSendBatchRetryLimit tests the retry limiting code in SendBatch
func TestSendBatchRetryLimit(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	messagesReceived := 0
	httpmock.RegisterResponder("POST", mockURL,
		func(req *http.Request) (*http.Response, error) {
			resp, err := httpmock.NewJsonResponse(500, nil)
			if err != nil {
				panic(err) // failure in test mocks
			}
			messagesReceived++
			return resp, nil
		},
	)

	msgBatches := [][]slackMessage{
		[]slackMessage{
			slackMessage{
				Channel:  "#slack-down",
				Icon:     "slack-down",
				Username: "slack-down",
				Text:     "All your monies are belong to us",
			},
		},
	}
	batch := [][]byte{}
	for _, msg := range msgBatches {
		enc, err := json.Marshal(msg)
		assert.NoError(t, err)

		batch = append(batch, enc)
	}

	sender := newSlackOutput("test", mockURL, 1, 1)
	sender.retryLimit = 3
	err := sender.SendBatch(batch, "all")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Retry limit")
	assert.Equal(t, 4, messagesReceived)
}

// TestSendBatchInternalRateLimit tests the internal rate limiting of SendBatch
func TestSendBatchInternalRateLimit(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	messageTimestamps := []time.Time{}
	httpmock.RegisterResponder("POST", mockURL,
		func(req *http.Request) (*http.Response, error) {
			messageTimestamps = append(messageTimestamps, time.Now())

			resp, err := httpmock.NewJsonResponse(200, nil)
			if err != nil {
				panic(err) // failure in test mocks
			}
			return resp, nil
		},
	)

	msgBatches := [][]slackMessage{
		[]slackMessage{
			slackMessage{
				Channel:  "#slack-down",
				Icon:     "slack-down",
				Username: "slack-down",
				Text:     "All your monies are belong to us",
			},
		},
	}

	batch := [][]byte{}
	for _, msg := range msgBatches {
		enc, err := json.Marshal(msg)
		assert.NoError(t, err)

		batch = append(batch, enc)
	}

	sender := newSlackOutput("test", mockURL, 1, 1)
	sender.retryLimit = 3

	for x := 0; x < 4; x++ {
		err := sender.SendBatch(batch, "all")
		assert.NoError(t, err)
	}

	// 4 messages sent (at a burst limit of 1 per sec) should mean at least
	// 3 secs have passed
	delta := messageTimestamps[len(messageTimestamps)-1].Sub(messageTimestamps[0])
	assert.True(t,
		delta+100*time.Millisecond >= time.Duration(3*time.Second), // Adding fuzz factor
		"Elapsed time '%v' should be more than 3 seconds", delta)
}

// TestSendBatchRateLimitResponse tests the response handling when SendBatch is rate limited by Slack
func TestSendBatchRateLimitResponse(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	messageTimestamps := []time.Time{}
	httpmock.RegisterResponder("POST", mockURL,
		func(req *http.Request) (*http.Response, error) {
			messageTimestamps = append(messageTimestamps, time.Now())

			// Rate limit the first request, then let the retry and the
			// second request through
			resp, err := httpmock.NewJsonResponse(200, nil)
			if len(messageTimestamps) < 2 {
				resp, err = httpmock.NewJsonResponse(429, nil)
				resp.Header.Set("Retry-After", "3")
			}
			if err != nil {
				panic(err) // failure in test mocks
			}
			return resp, nil
		},
	)

	msgBatches := [][]slackMessage{
		[]slackMessage{
			slackMessage{
				Channel:  "#rate-limit",
				Icon:     "rate-limit",
				Username: "rate-limit",
				Text:     "All your monies are belong to us",
			},
		},
	}

	batch := [][]byte{}
	for _, msg := range msgBatches {
		enc, err := json.Marshal(msg)
		assert.NoError(t, err)

		batch = append(batch, enc)
	}

	sender := newSlackOutput("test", mockURL, 1, 1)
	sender.retryLimit = 3

	for x := 0; x < 2; x++ {
		err := sender.SendBatch(batch, "all")
		assert.NoError(t, err)
	}

	// 2 messages sent with the first hitting a 3 second retry delay should mean at least
	// 3 secs have passed and there should be 3 calls to slack
	delta := messageTimestamps[len(messageTimestamps)-1].Sub(messageTimestamps[0])
	assert.Equal(t, 3, len(messageTimestamps))
	assert.True(t, delta >= time.Duration(3*time.Second), "Elapsed time '%v' should be more than 3 seconds", delta)
}

// TestSendBatchError tests the error handling of SendBatch()
func TestSendBatchError(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	messagesReceived := 0
	httpmock.RegisterResponder("POST", mockURL,
		func(req *http.Request) (*http.Response, error) {
			// Fails with a 500 twice, then fails with 404s
			messagesReceived++
			code := 500
			if messagesReceived > 2 {
				code = 404
			}

			resp, err := httpmock.NewJsonResponse(code, nil)
			if err != nil {
				panic(err) // failure in test mocks
			}
			return resp, nil
		},
	)

	msgBatches := [][]slackMessage{
		[]slackMessage{
			slackMessage{
				Channel:  "#monopoly",
				Icon:     "Do not pass Go",
				Username: "Do not pass Go",
				Text:     "All your monies are belong to us",
			},
		},
	}

	batch := [][]byte{}
	for _, msg := range msgBatches {
		enc, err := json.Marshal(msg)
		assert.NoError(t, err)

		batch = append(batch, enc)
	}

	t.Log("Expect a 500 first (this will be retried)")
	sender := newSlackOutput("test", mockURL, 1, 1)
	sender.retryLimit = 1
	err := sender.SendBatch(batch, "all")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "500")
	assert.Equal(t, 2, messagesReceived)

	t.Log("Expects a 404 (no retries)")
	sender = newSlackOutput("test", mockURL, 1, 1)
	sender.retryLimit = 3
	err = sender.SendBatch(batch, "all")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "404")
	assert.Equal(t, 4, messagesReceived)

	t.Log("Unknown channels should be skipped")
	err = sender.SendBatch(batch, "all")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Attempted to send message to unknown channel")
	assert.Equal(t, 4, messagesReceived)

	t.Log("After 2 hours unknown channels shouldn't be ignored")
	sender.unknownChannels["#monopoly"] = time.Now().Add(-2 * time.Hour)
	err = sender.SendBatch(batch, "all")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "404")
	assert.Equal(t, 5, messagesReceived)
}

func TestChannelThrottling(t *testing.T) {
	assert := assert.New(t)

	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	messagesReceived := 0
	var lastMessage slackMessage
	httpmock.RegisterResponder("POST", mockURL,
		func(req *http.Request) (*http.Response, error) {
			messagesReceived++

			err := json.NewDecoder(req.Body).Decode(&lastMessage)
			if err != nil {
				panic(err) // failure in test mocks
			}

			resp, err := httpmock.NewJsonResponse(200, nil)
			if err != nil {
				panic(err) // failure in test mocks
			}
			return resp, nil
		},
	)

	msgBatches := [][]slackMessage{
		[]slackMessage{
			slackMessage{
				Channel:  "#monopoly",
				Icon:     "Do not pass Go",
				Username: "Do not pass Go",
				Text:     "All your monies are belong to us",
			},
		},
	}

	batch := [][]byte{}
	for _, msg := range msgBatches {
		enc, err := json.Marshal(msg)
		assert.NoError(err)

		batch = append(batch, enc)
	}

	sender := newSlackOutput("test", mockURL, 1, 1)
	sender.retryLimit = 1
	for i := 0; i < 4; i++ {
		err := sender.SendBatch(batch, "all")
		assert.NoError(err)
		assert.Equal(messagesReceived, i+1)
		assert.NotContains(lastMessage.Text, "are being throttled :sixgod:")
	}

	err := sender.SendBatch(batch, "all")
	assert.NoError(err)
	assert.Equal(messagesReceived, 5)
	assert.Contains(lastMessage.Text, "are being throttled :sixgod:")

	err = sender.SendBatch(batch, "all")
	assert.Error(err)
	assert.Contains(err.Error(), "Message to channel throttled")
	assert.Equal(messagesReceived, 5)
}

// TestNotificationSendBatchParse tests that notification-service alerts get sent with parse = none.
func TestNotificationSendBatchParse(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	t.Log("a fake batch of notification-service messages")

	msgs := [][]slackMessage{
		[]slackMessage{
			slackMessage{
				Channel:  "#channelA",
				Icon:     ":fire:",
				Username: "fire",
				Text:     "mo money mo problems",
			},
			slackMessage{
				Channel:  "#channelB",
				Icon:     ":fire:",
				Username: ":fire:",
				Text:     "learning.com",
			},
			slackMessage{
				Channel:  "#notification-catcher",
				Icon:     ":fire:",
				Username: "notice",
				Text:     "fire",
			},
		},
	}

	batch := [][]byte{}
	for _, msg := range msgs {
		enc, err := json.Marshal(msg)
		assert.NoError(t, err)

		batch = append(batch, enc)
	}

	messagesReceived := 0
	httpmock.RegisterResponder("POST", mockURL,
		func(req *http.Request) (*http.Response, error) {
			t.Log("Verify expected headers")
			assert.Equal(t, "application/json", req.Header["Content-Type"][0])

			t.Log("Verify expected JSON body")
			decoder := json.NewDecoder(req.Body)
			var msg slackMessage
			err := decoder.Decode(&msg)
			assert.NoError(t, err)

			oriMsg := msgs[0][messagesReceived]
			assert.Equal(t, oriMsg.Text, msg.Text)
			assert.Equal(t, oriMsg.Channel, msg.Channel)
			assert.Equal(t, oriMsg.Icon, msg.Icon)
			assert.Equal(t, oriMsg.Username, msg.Username)

			if oriMsg.Channel == "#notification-catcher" {
				assert.Equal(t, "none", msg.Parse)
				assert.Equal(t, "notice", msg.Username)
			} else {
				assert.Equal(t, "", msg.Parse)
			}

			resp, err := httpmock.NewJsonResponse(200, nil)
			if err != nil {
				panic(err) // failure in test mocks
			}
			messagesReceived++
			return resp, nil
		},
	)

	sender := newSlackOutput("test", mockURL, 1, 1)
	sender.retryLimit = 3
	err := sender.SendBatch(batch, "all")
	assert.NoError(t, err)
	assert.Equal(t, 3, messagesReceived)
}
