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
	sender := newSlackOutput("test", mockURL, 1, 1, 3)

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
	sender := newSlackOutput("test", mockURL, 1, 1, 3)

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

	sender := newSlackOutput("test", mockURL, 1, 1, 3)

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
	sender := newSlackOutput("test", mockURL, 1, 1, 3)

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
	expectedTag, err := json.Marshal(slackTag{
		Channel:  "#test",
		Username: "testbot",
		Icon:     ":bot:",
	})
	assert.NoError(t, err)

	output, tags, err := sender.encodeMessage(input)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(tags))
	assert.Equal(t, string(expectedTag), tags[0])
	assert.Equal(t, "Hello World", string(output))

	t.Log("Multiple routes")
	input = map[string]interface{}{
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
				map[string]interface{}{
					"type":    "notifications",
					"channel": "#test2",
					"message": "Hello World",
					"user":    "testbot2",
					"icon":    ":bot2:",
				},
			},
		},
	}
	expectedTagA, err := json.Marshal(slackTag{
		Channel:  "#test",
		Username: "testbot",
		Icon:     ":bot:",
	})
	assert.NoError(t, err)
	expectedTagB, err := json.Marshal(slackTag{
		Channel:  "#test2",
		Username: "testbot2",
		Icon:     ":bot2:",
	})

	output, tags, err = sender.encodeMessage(input)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(tags))
	assert.Equal(t, string(expectedTagA), tags[0])
	assert.Equal(t, string(expectedTagB), tags[1])
	assert.Equal(t, "Hello World", string(output))

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
	sender := newSlackOutput("test", mockURL, 1, 1, 3)

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
	assert.Contains(t, err.Error(), "exceeds maximum length")
}

// TestSendBatch tests the nominal expected behavior of SendBatch()
func TestSendBatch(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	t.Log("a fake batch of slack messages")
	tagA := slackTag{
		Channel:  "#test",
		Username: "Tyler",
		Icon:     ":tyler:",
	}
	encTagA, _ := json.Marshal(tagA)

	msgs := []string{
		"All your schools are belong to us",
		"All your apps are belong to us",
		"All your monies are belong to us",
	}

	batch := [][]byte{
		[]byte(msgs[0]),
		[]byte(msgs[1]),
		[]byte(msgs[2]),
	}
	expectedMessage := strings.Join(msgs, "\n")

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

			assert.Equal(t, expectedMessage, msg.Text)
			assert.Equal(t, tagA.Channel, msg.Channel)
			assert.Equal(t, tagA.Icon, msg.Icon)
			assert.Equal(t, tagA.Username, msg.Username)
			assert.Equal(t, "", msg.Parse)

			resp, err := httpmock.NewJsonResponse(200, nil)
			if err != nil {
				panic(err) // failure in test mocks
			}
			messagesReceived++
			return resp, nil
		},
	)

	sender := newSlackOutput("test", mockURL, 1, 1, 3)
	err := sender.SendBatch(batch, string(encTagA))
	assert.NoError(t, err)
	assert.Equal(t, 1, messagesReceived)
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

	tag, _ := json.Marshal(slackTag{
		Channel:  "#flares",
		Username: "slackbot",
		Icon:     ":slack-hash:",
	})
	batch := [][]byte{
		[]byte("slack is down"),
	}
	sender := newSlackOutput("test", mockURL, 1, 1, 3)
	err := sender.SendBatch(batch, string(tag))
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

	tag, _ := json.Marshal(slackTag{
		Channel:  "#groundhogs",
		Username: "Phil",
		Icon:     ":bill-murray:",
	})
	batch := [][]byte{
		[]byte("Hello Again"),
	}
	sender := newSlackOutput("test", mockURL, 1, 1, 3)

	for x := 0; x < 4; x++ {
		err := sender.SendBatch(batch, string(tag))
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

	tag, _ := json.Marshal(slackTag{
		Channel:  "#groundhogs",
		Username: "Phil",
		Icon:     ":bill-murray:",
	})
	batch := [][]byte{
		[]byte("Hello again"),
	}
	sender := newSlackOutput("test", mockURL, 1, 1, 3)

	for x := 0; x < 2; x++ {
		err := sender.SendBatch(batch, string(tag))
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

	tag, _ := json.Marshal(slackTag{
		Channel:  "#monopoly",
		Username: "Player1",
		Icon:     ":top-hat:",
	})
	batch := [][]byte{
		[]byte("Do not pass Go"),
	}

	t.Log("Expect a 500 first (this will be retried)")
	sender := newSlackOutput("test", mockURL, 1, 1, 1)
	err := sender.SendBatch(batch, string(tag))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "500")
	assert.Equal(t, 2, messagesReceived)

	t.Log("Expects a 404 (no retries)")
	sender = newSlackOutput("test", mockURL, 1, 1, 3)
	err = sender.SendBatch(batch, string(tag))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "404")
	assert.Equal(t, 4, messagesReceived)

	t.Log("Unknown channels should be skipped")
	err = sender.SendBatch(batch, string(tag))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Attempted to send message to unknown channel")
	assert.Equal(t, 4, messagesReceived)

	t.Log("After 2 hours unknown channels shouldn't be ignored")
	sender.unknownChannels["#monopoly"] = time.Now().Add(-2 * time.Hour)
	err = sender.SendBatch(batch, string(tag))
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
	tag, _ := json.Marshal(slackTag{
		Channel:  "#monopoly",
		Username: "Player1",
		Icon:     ":top-hat:",
	})
	batch := [][]byte{
		[]byte("Do not pass Go"),
	}

	sender := newSlackOutput("test", mockURL, 1, 1, 1)
	for i := 0; i < 4; i++ {
		err := sender.SendBatch(batch, string(tag))
		assert.NoError(err)
		assert.Equal(messagesReceived, i+1)
		assert.NotContains(lastMessage.Text, "is being throttled :sixgod:")
	}

	err := sender.SendBatch(batch, string(tag))
	assert.NoError(err)
	assert.Equal(messagesReceived, 5)
	assert.Contains(lastMessage.Text, "is being throttled :sixgod:")

	err = sender.SendBatch(batch, string(tag))
	assert.Error(err)
	assert.Contains(err.Error(), "Message to channel throttled")
	assert.Equal(messagesReceived, 5)
}

// TestNotificationSendBatchParse tests that notification-service alerts get sent with parse = none.
func TestNotificationSendBatchParse(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	t.Log("a fake batch of notification-service messages")
	tagA := slackTag{
		Channel:  "#notification-catcher",
		Username: "notice",
		Icon:     ":notebook:",
	}
	encTagA, _ := json.Marshal(tagA)

	msgs := []string{
		"mo money mo problems",
		"learning.com",
		"#notification-catcher",
	}

	batch := [][]byte{
		[]byte(msgs[0]),
		[]byte(msgs[1]),
		[]byte(msgs[2]),
	}
	expectedMessage := strings.Join(msgs, "\n")

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

			assert.Equal(t, expectedMessage, msg.Text)
			assert.Equal(t, tagA.Channel, msg.Channel)
			assert.Equal(t, tagA.Icon, msg.Icon)
			assert.Equal(t, tagA.Username, msg.Username)
			assert.Equal(t, "none", msg.Parse)

			resp, err := httpmock.NewJsonResponse(200, nil)
			if err != nil {
				panic(err) // failure in test mocks
			}
			messagesReceived++
			return resp, nil
		},
	)

	sender := newSlackOutput("test", mockURL, 1, 1, 3)
	err := sender.SendBatch(batch, string(encTagA))
	assert.NoError(t, err)
	assert.Equal(t, 1, messagesReceived)
}
