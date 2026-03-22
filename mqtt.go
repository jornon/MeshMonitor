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
	mqttOnce   sync.Once
)

// connectMQTT lazily connects to the broker on the first publish.
func connectMQTT() error {
	var connErr error
	mqttOnce.Do(func() {
		broker := fmt.Sprintf("tcp://%s:%d", cfg.MQTTHost, cfg.MQTTPort)
		opts := mqtt.NewClientOptions().
			AddBroker(broker).
			SetClientID(fmt.Sprintf("meshmonitor-%d", time.Now().UnixNano()%100000)).
			SetAutoReconnect(true).
			SetConnectRetry(true).
			SetConnectRetryInterval(5 * time.Second)

		mqttClient = mqtt.NewClient(opts)
		token := mqttClient.Connect()
		if token.WaitTimeout(10 * time.Second); token.Error() != nil {
			connErr = fmt.Errorf("mqtt connect: %w", token.Error())
			mqttOnce = sync.Once{} // allow retry
			return
		}
		ui.Success("MQTT connected to %s", broker)
	})
	return connErr
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
	token := mqttClient.Publish(topic, 1, false, data)
	if token.WaitTimeout(5 * time.Second); token.Error() != nil {
		return fmt.Errorf("mqtt publish: %w", token.Error())
	}
	ui.Dimf("[mqtt] → %s (%d bytes)\n", topic, len(data))
	return nil
}
