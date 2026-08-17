package middleware

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

var incrementWithTTLScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return count
`)

var incrementQuotaCountersScript = redis.NewScript(`
local key_index = 1
local minute_count = -1
local daily_count = -1

if tonumber(ARGV[1]) == 1 then
  minute_count = redis.call('INCR', KEYS[key_index])
  if minute_count == 1 then
    redis.call('PEXPIRE', KEYS[key_index], ARGV[2])
  end
  key_index = key_index + 1
  if minute_count > tonumber(ARGV[3]) then
    return {minute_count, daily_count, 1}
  end
end

if tonumber(ARGV[4]) == 1 then
  daily_count = redis.call('INCR', KEYS[key_index])
  if daily_count == 1 then
    redis.call('PEXPIRE', KEYS[key_index], ARGV[5])
  end
  if daily_count > tonumber(ARGV[6]) then
    return {minute_count, daily_count, 2}
  end
end

return {minute_count, daily_count, 0}
`)

type quotaCounterResult struct {
	MinuteCount int64
	DailyCount  int64
	RejectedBy  int64
}

func incrementWithTTL(ctx context.Context, rdb *redis.Client, key string, ttl time.Duration) (int64, error) {
	if ttl < time.Second {
		ttl = time.Second
	}
	return incrementWithTTLScript.Run(ctx, rdb, []string{key}, ttl.Milliseconds()).Int64()
}

func incrementQuotaCounters(
	ctx context.Context,
	rdb *redis.Client,
	minuteKey string,
	minuteTTL time.Duration,
	minuteLimit int,
	dailyKey string,
	dailyTTL time.Duration,
	dailyLimit int,
) (quotaCounterResult, error) {
	result := quotaCounterResult{MinuteCount: -1, DailyCount: -1}
	keys := make([]string, 0, 2)
	minuteEnabled := minuteLimit > 0 && minuteKey != ""
	dailyEnabled := dailyLimit > 0 && dailyKey != ""
	if !minuteEnabled && !dailyEnabled {
		return result, nil
	}
	if minuteTTL < time.Second {
		minuteTTL = time.Second
	}
	if dailyTTL < time.Second {
		dailyTTL = time.Second
	}
	if minuteEnabled {
		keys = append(keys, minuteKey)
	}
	if dailyEnabled {
		keys = append(keys, dailyKey)
	}
	values, err := incrementQuotaCountersScript.Run(ctx, rdb, keys,
		boolInt64(minuteEnabled), minuteTTL.Milliseconds(), minuteLimit,
		boolInt64(dailyEnabled), dailyTTL.Milliseconds(), dailyLimit,
	).Int64Slice()
	if err != nil {
		return result, err
	}
	if len(values) == 3 {
		result.MinuteCount = values[0]
		result.DailyCount = values[1]
		result.RejectedBy = values[2]
	}
	return result, nil
}

func boolInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
