package merge

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/AdguardTeam/AdGuardHome/internal/stats"
)

const topLimit = 10

// Stats sums compatible statistics responses and adds cluster health metadata.
func Stats(sources []NodeData, degraded []string) ([]byte, error) {
	if len(sources) == 0 {
		return nil, errors.New("statistics requires at least one response")
	}

	items := make([]stats.StatsResp, len(sources))
	for i, source := range sources {
		if err := json.Unmarshal(source.Data, &items[i]); err != nil {
			return nil, fmt.Errorf("decoding %s statistics: %w", source.Name, err)
		}
	}

	merged := stats.StatsResp{TimeUnits: items[0].TimeUnits}
	merged.DNSQueries = mergeSeries(items, func(item stats.StatsResp) []uint64 { return item.DNSQueries })
	merged.BlockedFiltering = mergeSeries(items, func(item stats.StatsResp) []uint64 { return item.BlockedFiltering })
	merged.ReplacedSafebrowsing = mergeSeries(items, func(item stats.StatsResp) []uint64 {
		return item.ReplacedSafebrowsing
	})
	merged.ReplacedParental = mergeSeries(items, func(item stats.StatsResp) []uint64 { return item.ReplacedParental })

	var processingTime float64
	var queries uint64
	topQueried := map[string]uint64{}
	topClients := map[string]uint64{}
	topBlocked := map[string]uint64{}
	upstreamResponses := map[string]uint64{}
	upstreamWeightedTime := map[string]float64{}
	for _, item := range items {
		merged.NumDNSQueries += item.NumDNSQueries
		merged.NumBlockedFiltering += item.NumBlockedFiltering
		merged.NumReplacedSafebrowsing += item.NumReplacedSafebrowsing
		merged.NumReplacedSafesearch += item.NumReplacedSafesearch
		merged.NumReplacedParental += item.NumReplacedParental
		processingTime += item.AvgProcessingTime * float64(item.NumDNSQueries)
		queries += item.NumDNSQueries
		mergeCounts(topQueried, item.TopQueried)
		mergeCounts(topClients, item.TopClients)
		mergeCounts(topBlocked, item.TopBlocked)

		responses := flattenCounts(item.TopUpstreamsResponses)
		averages := flattenAverages(item.TopUpstreamsAvgTime)
		for upstream, count := range responses {
			upstreamResponses[upstream] += count
			upstreamWeightedTime[upstream] += averages[upstream] * float64(count)
		}
	}
	if queries > 0 {
		merged.AvgProcessingTime = processingTime / float64(queries)
	}
	merged.TopQueried = rankCounts(topQueried)
	merged.TopClients = rankCounts(topClients)
	merged.TopBlocked = rankCounts(topBlocked)
	merged.TopUpstreamsResponses = rankCounts(upstreamResponses)
	upstreamAverages := map[string]float64{}
	for upstream, weighted := range upstreamWeightedTime {
		if count := upstreamResponses[upstream]; count > 0 {
			upstreamAverages[upstream] = weighted / float64(count)
		}
	}
	merged.TopUpstreamsAvgTime = rankAverages(upstreamAverages)

	numOK, degradedNodes := metadata(len(sources), degraded)
	return json.Marshal(struct {
		*stats.StatsResp
		NumNodesOK    int      `json:"num_nodes_ok"`
		DegradedNodes []string `json:"degraded_nodes"`
	}{&merged, numOK, degradedNodes})
}

func mergeSeries(items []stats.StatsResp, selectSeries func(stats.StatsResp) []uint64) []uint64 {
	minimum := -1
	for _, item := range items {
		length := len(selectSeries(item))
		if minimum < 0 || length < minimum {
			minimum = length
		}
	}
	if minimum <= 0 {
		return []uint64{}
	}

	result := make([]uint64, minimum)
	for _, item := range items {
		series := selectSeries(item)
		offset := len(series) - minimum
		for i := 0; i < minimum; i++ {
			result[i] += series[offset+i]
		}
	}

	return result
}

func mergeCounts(target map[string]uint64, items []map[string]uint64) {
	for key, value := range flattenCounts(items) {
		target[key] += value
	}
}

func flattenCounts(items []map[string]uint64) map[string]uint64 {
	result := map[string]uint64{}
	for _, item := range items {
		for key, value := range item {
			result[key] += value
		}
	}

	return result
}

func flattenAverages(items []map[string]float64) map[string]float64 {
	result := map[string]float64{}
	for _, item := range items {
		for key, value := range item {
			result[key] = value
		}
	}

	return result
}

func rankCounts(counts map[string]uint64) []map[string]uint64 {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] == counts[keys[j]] {
			return keys[i] < keys[j]
		}
		return counts[keys[i]] > counts[keys[j]]
	})
	if len(keys) > topLimit {
		keys = keys[:topLimit]
	}
	result := make([]map[string]uint64, 0, len(keys))
	for _, key := range keys {
		result = append(result, map[string]uint64{key: counts[key]})
	}

	return result
}

func rankAverages(averages map[string]float64) []map[string]float64 {
	keys := make([]string, 0, len(averages))
	for key := range averages {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if averages[keys[i]] == averages[keys[j]] {
			return keys[i] < keys[j]
		}
		return averages[keys[i]] > averages[keys[j]]
	})
	if len(keys) > topLimit {
		keys = keys[:topLimit]
	}
	result := make([]map[string]float64, 0, len(keys))
	for _, key := range keys {
		result = append(result, map[string]float64{key: averages[key]})
	}

	return result
}

// StatsConfig preserves local fields and exposes the shortest healthy window.
func StatsConfig(sources []NodeData, degraded []string) ([]byte, error) {
	if len(sources) == 0 {
		return nil, errors.New("statistics config requires at least one response")
	}

	var merged map[string]any
	minimum := float64(0)
	for i, source := range sources {
		object, err := decodeObject(source.Data)
		if err != nil {
			return nil, fmt.Errorf("decoding %s statistics config: %w", source.Name, err)
		}
		if i == 0 {
			merged = object
		}
		interval, ok := object["interval"].(float64)
		if ok && interval > 0 && (minimum == 0 || interval < minimum) {
			minimum = interval
		}
	}
	if minimum > 0 {
		merged["interval"] = minimum
	}
	merged["num_nodes_ok"], merged["degraded_nodes"] = metadata(len(sources), degraded)

	return json.Marshal(merged)
}
