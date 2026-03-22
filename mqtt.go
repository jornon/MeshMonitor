package main

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

var (
	mqttClient mqtt.Client
	mqttMu     sync.Mutex
	mqttReady  bool
)

// mqttUsername is set at startup from the device config endpoint.
var mqttUsername string

// connectMQTT connects to the broker, retrying on each call if a previous attempt failed.
func connectMQTT() error {
	mqttMu.Lock()
	defer mqttMu.Unlock()

	if mqttReady {
		return nil
	}

	broker := fmt.Sprintf("tcp://%s:%d", cfg.MQTTHost, cfg.MQTTPort)
	opts := mqtt.NewClientOptions().
		AddBroker(broker).
		SetClientID(fmt.Sprintf("meshmonitor-%d", time.Now().UnixNano()%100000)).
		SetAutoReconnect(true).
		SetConnectRetry(false)

	if mqttUsername != "" {
		opts.SetUsername(mqttUsername)
		opts.SetPassword(cfg.ServerToken)
	}

	mqttClient = mqtt.NewClient(opts)
	token := mqttClient.Connect()
	ok := token.WaitTimeout(10 * time.Second)
	if !ok {
		return fmt.Errorf("mqtt connect: timeout connecting to %s", broker)
	}
	if token.Error() != nil {
		return fmt.Errorf("mqtt connect: %w", token.Error())
	}
	mqttReady = true
	ui.Verb("MQTT connected to %s as %s", broker, mqttUsername)
	return nil
}

// PublishStatus publishes a repeater status report to the MQTT broker.
func PublishStatus(target RepeaterTarget, status *StatusResponse) error {
	topic := fmt.Sprintf("%s/%s/status", cfg.MQTTTopicPrefix, status.PubKeyPrefix)
	payload := map[string]any{
		"name":             target.Name,
		"pub_key_prefix":   status.PubKeyPrefix,
		"batt_milli_volts": status.BattMilliVolts,
		"tx_queue_len":     status.TxQueueLen,
		"noise_floor_dbm":  status.NoiseFloor,
		"last_rssi_dbm":    status.LastRSSI,
		"last_snr_db":      float64(status.LastSNRx4) / 4.0,
		"packets_recv":     status.PacketsRecv,
		"packets_sent":     status.PacketsSent,
		"air_time_secs":    status.AirTimeSecs,
		"up_time_secs":     status.UpTimeSecs,
		"sent_flood":       status.SentFlood,
		"sent_direct":      status.SentDirect,
		"recv_flood":       status.RecvFlood,
		"recv_direct":      status.RecvDirect,
		"err_events":       status.ErrEvents,
		"direct_dups":      status.DirectDups,
		"flood_dups":       status.FloodDups,
		"rx_air_time_secs": status.RxAirTimeSecs,
		"recv_errors":      status.RecvErrors,
	}
	return publish(topic, payload)
}

// PublishTelemetry publishes raw CayenneLPP telemetry data to the MQTT broker.
func PublishTelemetry(target RepeaterTarget, telem *TelemetryResponse) error {
	topic := fmt.Sprintf("%s/%s/telemetry", cfg.MQTTTopicPrefix, telem.PubKeyPrefix)
	payload := map[string]any{
		"name":           target.Name,
		"pub_key_prefix": telem.PubKeyPrefix,
		"cayenne_lpp":    telem.RawHex,
	}
	return publish(topic, payload)
}

func publish(topic string, payload any) error {
	if err := connectMQTT(); err != nil {
		return err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	broker := fmt.Sprintf("tcp://%s:%d", cfg.MQTTHost, cfg.MQTTPort)
	token := mqttClient.Publish(topic, 1, false, data)
	ok := token.WaitTimeout(5 * time.Second)
	if token.Error() != nil {
		ui.Verb("[mqtt] FAIL %s → %s: %v", broker, topic, token.Error())
		return fmt.Errorf("mqtt publish: %w", token.Error())
	}
	if !ok {
		ui.Verb("[mqtt] FAIL %s → %s: timeout (not delivered)", broker, topic)
		return fmt.Errorf("mqtt publish timeout for %s", topic)
	}
	ui.Verb("[mqtt] OK %s → %s (%d bytes)", broker, topic, len(data))
	return nil
}
