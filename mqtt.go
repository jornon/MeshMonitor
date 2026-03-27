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
// If contactGPS is non-nil, latitude and longitude are included.
func PublishStatus(target RepeaterTarget, status *StatusResponse, contactGPS *[2]float64) error {
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
	if contactGPS != nil {
		payload["latitude"] = contactGPS[0]
		payload["longitude"] = contactGPS[1]
	}
	return publish(topic, payload)
}

// PublishTelemetry publishes decoded CayenneLPP telemetry data to the MQTT broker.
// GPS from CayenneLPP takes priority; contactGPS is used as fallback.
func PublishTelemetry(target RepeaterTarget, telem *TelemetryResponse, contactGPS *[2]float64) error {
	topic := fmt.Sprintf("%s/%s/telemetry", cfg.MQTTTopicPrefix, telem.PubKeyPrefix)
	payload := map[string]any{
		"name":           target.Name,
		"pub_key_prefix": telem.PubKeyPrefix,
		"cayenne_lpp":    telem.RawHex,
	}
	// Decode CayenneLPP and add individual fields.
	hasLPPGPS := false
	if len(telem.RawData) > 0 {
		decoded := DecodeCayenneLPP(telem.RawData)
		for k, v := range CayenneToMap(decoded) {
			payload[k] = v
		}
		// Check for CayenneLPP GPS data.
		gps := DecodeCayenneGPS(telem.RawData)
		if gps != nil {
			payload["latitude"] = gps[0]
			payload["longitude"] = gps[1]
			payload["altitude"] = gps[2]
			hasLPPGPS = true
		}
	}
	// Fall back to contact GPS if no CayenneLPP GPS.
	if !hasLPPGPS && contactGPS != nil {
		payload["latitude"] = contactGPS[0]
		payload["longitude"] = contactGPS[1]
	}
	return publish(topic, payload)
}

// PublishNeighbours publishes a repeater's neighbour list to the MQTT broker.
// contactsByPrefix is used to resolve 4-byte neighbour prefixes to full public keys.
func PublishNeighbours(target RepeaterTarget, neighbours []NeighbourEntry, contactsByPrefix map[string]string) error {
	topic := fmt.Sprintf("%s/%s/neighbours", cfg.MQTTTopicPrefix, target.PublicKey[:12])
	type neighbourJSON struct {
		PublicKey string  `json:"public_key"`
		SecsAgo   int32   `json:"secs_ago"`
		SNR       float64 `json:"snr_db"`
	}
	var entries []neighbourJSON
	for _, n := range neighbours {
		fullKey := contactsByPrefix[n.PubKeyPrefix]
		if fullKey == "" {
			fullKey = n.PubKeyPrefix // fallback to prefix if not resolved
		}
		entries = append(entries, neighbourJSON{
			PublicKey: fullKey,
			SecsAgo:   n.SecsAgo,
			SNR:       n.SNR,
		})
	}
	payload := map[string]any{
		"name":       target.Name,
		"public_key": target.PublicKey,
		"neighbours": entries,
	}
	return publish(topic, payload)
}

// PublishCompanionStats publishes the companion device's own status.
func PublishCompanionStats(battMV uint16, selfInfo *SelfInfo) error {
	topic := fmt.Sprintf("%s/companion/status", cfg.MQTTTopicPrefix)
	payload := map[string]any{
		"name":             selfInfo.Name,
		"pub_key":          selfInfo.PublicKeyHex,
		"batt_milli_volts": battMV,
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
	ok := token.WaitTimeout(5 * time.Second)
	if token.Error() != nil {
		ui.Dimf("     📤 ❌ %s: %v\n", topic, token.Error())
		resetMQTT() // force reconnect on next publish
		return fmt.Errorf("mqtt publish: %w", token.Error())
	}
	if !ok {
		ui.Dimf("     📤 ❌ %s: timeout\n", topic)
		resetMQTT() // force reconnect on next publish
		return fmt.Errorf("mqtt publish timeout for %s", topic)
	}
	ui.Dimf("     📤 %s (%d bytes)\n", topic, len(data))
	return nil
}

// resetMQTT tears down the current MQTT connection so that the next
// publish triggers a fresh connectMQTT(). This handles the case where
// the underlying TCP connection has died but mqttReady is still true.
func resetMQTT() {
	mqttMu.Lock()
	defer mqttMu.Unlock()
	if mqttClient != nil {
		mqttClient.Disconnect(250)
	}
	mqttReady = false
	ui.Warn("MQTT connection reset — will reconnect on next publish")
}
