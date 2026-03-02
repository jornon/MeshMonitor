package main

import (
	"encoding/json"
	"fmt"
)

// ---------------------------------------------------------------------------
// Mocked MQTT client
// ---------------------------------------------------------------------------

// PublishStatus publishes a repeater status report to the MQTT broker.
// Currently MOCKED — logs the payload to stdout.
func PublishStatus(target RepeaterTarget, status *StatusResponse) error {
	topic := fmt.Sprintf("%s/%s/status", cfg.MQTTTopicPrefix, status.PubKeyPrefix)
	payload := map[string]any{
		"name":                  target.Name,
		"pub_key_prefix":        status.PubKeyPrefix,
		"batt_milli_volts":      status.BattMilliVolts,
		"tx_queue_len":          status.TxQueueLen,
		"noise_floor_dbm":       status.NoiseFloor,
		"last_rssi_dbm":         status.LastRSSI,
		"last_snr_db":           float64(status.LastSNRx4) / 4.0,
		"packets_recv":          status.PacketsRecv,
		"packets_sent":          status.PacketsSent,
		"air_time_secs":         status.AirTimeSecs,
		"up_time_secs":          status.UpTimeSecs,
		"sent_flood":            status.SentFlood,
		"sent_direct":           status.SentDirect,
		"recv_flood":            status.RecvFlood,
		"recv_direct":           status.RecvDirect,
		"err_events":            status.ErrEvents,
		"direct_dups":           status.DirectDups,
		"flood_dups":            status.FloodDups,
		"rx_air_time_secs":      status.RxAirTimeSecs,
		"recv_errors":           status.RecvErrors,
	}
	return mockPublish(topic, payload)
}

// PublishTelemetry publishes raw CayenneLPP telemetry data to the MQTT broker.
// Currently MOCKED — logs the payload to stdout.
func PublishTelemetry(target RepeaterTarget, telem *TelemetryResponse) error {
	topic := fmt.Sprintf("%s/%s/telemetry", cfg.MQTTTopicPrefix, telem.PubKeyPrefix)
	payload := map[string]any{
		"name":           target.Name,
		"pub_key_prefix": telem.PubKeyPrefix,
		"cayenne_lpp":    telem.RawHex,
	}
	return mockPublish(topic, payload)
}

func mockPublish(topic string, payload any) error {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	ui.Dimf("[mqtt] %s:%d → %s\n%s\n", cfg.MQTTHost, cfg.MQTTPort, topic, string(data))
	return nil
}
