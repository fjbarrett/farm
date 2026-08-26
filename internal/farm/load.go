package farm

import (
	"encoding/json"
	"fmt"
	"os"
)

func LoadInventory(path string) (Inventory, error) {
	var inventory Inventory
	if err := decodeJSONFile(path, &inventory); err != nil {
		return inventory, fmt.Errorf("load inventory: %w", err)
	}
	if inventory.Version != 1 {
		return inventory, fmt.Errorf("unsupported inventory version %d", inventory.Version)
	}
	seen := make(map[string]bool, len(inventory.Devices))
	for _, device := range inventory.Devices {
		if device.ID == "" || device.Name == "" || device.Transport == "" {
			return inventory, fmt.Errorf("each device requires id, name, and transport")
		}
		if seen[device.ID] {
			return inventory, fmt.Errorf("duplicate device id %q", device.ID)
		}
		seen[device.ID] = true
	}
	return inventory, nil
}

func LoadJob(path string) (Job, error) {
	var job Job
	if err := decodeJSONFile(path, &job); err != nil {
		return job, fmt.Errorf("load job: %w", err)
	}
	if err := ValidateJob(job); err != nil {
		return job, err
	}
	return job, nil
}

func DecodeJob(data []byte) (Job, error) {
	var job Job
	if err := json.Unmarshal(data, &job); err != nil {
		return job, fmt.Errorf("decode job: %w", err)
	}
	if err := ValidateJob(job); err != nil {
		return job, err
	}
	return job, nil
}

func decodeJSONFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}
