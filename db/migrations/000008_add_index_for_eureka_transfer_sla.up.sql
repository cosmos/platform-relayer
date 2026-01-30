CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_eureka_transfers_recv_time_chain_ids
ON eureka_transfers (
    recv_tx_time,
    source_chain_id,
    destination_chain_id
)
INCLUDE (source_tx_time);
