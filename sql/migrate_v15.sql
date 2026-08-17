-- v15: narrow the donation recalculation path to manual adjustment events.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_donation_reward_events_manual
    ON donation_reward_events (token_id)
    WHERE event_type = 'manual_adjust';
