package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
)

const mockURL = "https://mockery.com/call/me"

// TestEncodeMessage tests the encodeMessage() helper used in ProcessMessage()
func TestEncodeMessage(t *testing.T) {
	sender := newSlackOutput("test", mockURL, 0, 1, 1, 3)

	// Nominal case
	input := map[string]interface{}{
		"rawlog":          "slack message goes here",
		"_kvmeta.type":    "notifications",
		"_kvmeta.channel": "#test",
		"_kvmeta.message": "Hello World",
		"_kvmeta.user":    "testbot",
		"_kvmeta.icon":    ":bot:",
	}
	expected := slackMessage{
		Channel:  "#test",
		Text:     "Hello World",
		Username: "testbot",
		Icon:     ":bot:",
	}
	output, tags, err := sender.encodeMessage(input)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(tags))
	assert.Equal(t, "#test", tags[0])

	outputMessage := slackMessage{}
	err = json.Unmarshal(output, &outputMessage)
	assert.NoError(t, err)
	assert.Equal(t, expected, outputMessage)

	// Missing the raw log
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

	// Not a notification
	input = map[string]interface{}{
		"rawlog":          "slack message goes here",
		"_kvmeta.type":    "metric",
		"_kvmeta.channel": "#test",
		"_kvmeta.message": "Hello World",
		"_kvmeta.user":    "testbot",
		"_kvmeta.icon":    ":bot:",
	}
	output, tags, err = sender.encodeMessage(input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "intentionally skipped")

	// Missing a required field
	input = map[string]interface{}{
		"rawlog":          "slack message goes here",
		"_kvmeta.type":    "notifications",
		"_kvmeta.message": "Hello World",
		"_kvmeta.user":    "testbot",
		"_kvmeta.icon":    ":bot:",
	}
	output, tags, err = sender.encodeMessage(input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// Empty required field
	input = map[string]interface{}{
		"rawlog":          "slack message goes here",
		"_kvmeta.type":    "notifications",
		"_kvmeta.channel": "#test",
		"_kvmeta.message": "",
		"_kvmeta.user":    "testbot",
		"_kvmeta.icon":    ":bot:",
	}
	output, tags, err = sender.encodeMessage(input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "is empty")
}

// TestSendBatch tests the nominal expected behavior of SendBatch()
func TestSendBatch(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	// a fake batch of slack messages
	msgA := slackMessage{
		Channel:  "#test",
		Text:     "All your schools are belong to us",
		Username: "Tyler",
		Icon:     ":tyler:",
	}
	msgB := slackMessage{
		Channel:  "#test",
		Text:     "All your deploys are belong to us",
		Username: "Xavi",
		Icon:     ":noah:",
	}
	msgC := slackMessage{
		Channel:  "#test",
		Text:     "All your dogs are belong to us",
		Username: "Frankie",
		Icon:     ":leo:",
	}

	expectedMessages := map[string]slackMessage{
		"Tyler":   msgA,
		"Xavi":    msgB,
		"Frankie": msgC,
	}

	batch := [][]byte{}
	for _, msg := range expectedMessages {
		encodedMsg, _ := json.Marshal(msg)
		batch = append(batch, encodedMsg)
	}

	messagesReceived := 0
	httpmock.RegisterResponder("POST", mockURL,
		func(req *http.Request) (*http.Response, error) {
			// Verify expected headers
			assert.Equal(t, "application/json", req.Header["Content-Type"][0])

			// Verify expected JSON body
			decoder := json.NewDecoder(req.Body)
			var msg slackMessage
			err := decoder.Decode(&msg)
			assert.NoError(t, err)

			expected, ok := expectedMessages[msg.Username]
			assert.True(t, ok)
			assert.Equal(t, expected, msg)

			resp, err := httpmock.NewJsonResponse(200, nil)
			if err != nil {
				panic(err) // failure in test mocks
			}
			messagesReceived++
			return resp, nil
		},
	)

	sender := newSlackOutput("test", mockURL, 0, 1, 1, 3)
	err := sender.SendBatch(batch, "#test")
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

	msg, _ := json.Marshal(slackMessage{
		Channel:  "#flares",
		Text:     "slack is down",
		Username: "slackbot",
		Icon:     ":slack-hash:",
	})
	batch := [][]byte{
		msg,
	}
	sender := newSlackOutput("test", mockURL, 0, 1, 1, 3)
	err := sender.SendBatch(batch, "#test")
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

	msg, _ := json.Marshal(slackMessage{
		Channel:  "#groundhogs",
		Text:     "Hello again",
		Username: "Phil",
		Icon:     ":bill-murray:",
	})
	batch := [][]byte{
		msg,
		msg,
		msg,
		msg,
	}
	sender := newSlackOutput("test", mockURL, 0, 1, 1, 3)
	err := sender.SendBatch(batch, "#groundhogs")
	assert.NoError(t, err)

	// 4 messages sent (at a burst limit of 1 per sec) should mean at least
	// 3 secs have passed
	delta := messageTimestamps[len(messageTimestamps)-1].Sub(messageTimestamps[0])
	assert.True(t, delta >= time.Duration(3*time.Second), "Elapsed time '%v' should be more than 3 seconds", delta)
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

	msg, _ := json.Marshal(slackMessage{
		Channel:  "#groundhogs",
		Text:     "Hello again",
		Username: "Phil",
		Icon:     ":bill-murray:",
	})
	batch := [][]byte{
		msg,
		msg,
	}
	sender := newSlackOutput("test", mockURL, 0, 1, 1, 3)
	err := sender.SendBatch(batch, "#groundhogs")
	assert.NoError(t, err)

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

	msg, _ := json.Marshal(slackMessage{
		Channel:  "#monopoly",
		Text:     "Do not pass Go",
		Username: "Player1",
		Icon:     ":top-hat:",
	})
	batch := [][]byte{
		msg,
	}

	// Expect a 500 first (this will be retried)
	sender := newSlackOutput("test", mockURL, 0, 1, 1, 1)
	err := sender.SendBatch(batch, "#monopoly")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "500")
	assert.Equal(t, 2, messagesReceived)

	// Expects a 404 (no retries)
	sender = newSlackOutput("test", mockURL, 0, 1, 1, 3)
	err = sender.SendBatch(batch, "#monopoly")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "404")
	assert.Equal(t, 3, messagesReceived)
}
