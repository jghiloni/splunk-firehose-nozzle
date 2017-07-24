package drain

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"code.cloudfoundry.org/lager"
	"github.com/cloudfoundry-community/splunk-firehose-nozzle/splunk"
)

type LoggingSplunk struct {
	logger      lager.Logger
	client      splunk.SplunkClient
	flushWindow time.Duration               // time period before flushing 'LoggingSplunk.events'
	events      chan map[string]interface{} // data structure to store events for Splunk to index
}

func NewLoggingSplunk(logger lager.Logger, splunkClient splunk.SplunkClient, flushWindow time.Duration) *LoggingSplunk {
	return &LoggingSplunk{
		logger:      logger,
		client:      splunkClient,
		flushWindow: flushWindow,
		// FIXME, make buffer size 100 configurable
		events: make(chan map[string]interface{}, 100),
	}
}

func (l *LoggingSplunk) Connect() bool {
	go l.consume()

	return true
}

func (l *LoggingSplunk) ShipEvents(fields map[string]interface{}, msg string) {
	event := l.buildEvent(fields, msg)
	l.events <- event
}

func (l *LoggingSplunk) consume(client splunk.SplunkClient) {
	var batch []map[string]interface{}
	timer := time.NewTimer(l.config.FlushInterval)

	// Flush takes place when 1) batch limit is reached. 2)flush window expires
	for {
		select {
		case event := <-l.events:
			batch = append(batch, event)
			if len(batch) >= l.config.BatchSize {
				batch = l.indexEvents(client, batch)
				timer.Reset(l.config.FlushInterval) //reset channel timer
			}
		case <-timer.C:
			batch = l.indexEvents(client, batch)
			timer.Reset(l.config.FlushInterval)
		}
	}
}

func (l *LoggingSplunk) indexEvents(batch []map[string]interface{}) []map[string]interface{} {
	if len(batch) == 0 {
		return batch
	}

	l.logger.Info(fmt.Sprintf("Posting %d events", len(batch)))
	err := l.client.Post(batch)
	if err != nil {
		l.logger.Error("Unable to talk to Splunk, error=%+v", err)
		// return back the batch for next retry
		return batch
	}

	return nil
}

func (l *LoggingSplunk) buildEvent(fields map[string]interface{}, msg string) map[string]interface{} {
	if len(msg) > 0 {
		fields["msg"] = msg
	}
	event := map[string]interface{}{}

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	if val, ok := fields["timestamp"]; ok {
		timestamp = l.nanoSecondsToSeconds(val.(int64))
	}
	event["time"] = timestamp

	event["host"] = fields["ip"]
	event["source"] = fields["job"]

	eventType := strings.ToLower(fields["event_type"].(string))
	event["sourcetype"] = fmt.Sprintf("cf:%s", eventType)

	event["event"] = fields

	return event
}

func (l *LoggingSplunk) nanoSecondsToSeconds(nanoseconds int64) string {
	seconds := float64(nanoseconds) * math.Pow(1000, -3)
	return fmt.Sprintf("%.3f", seconds)
}
