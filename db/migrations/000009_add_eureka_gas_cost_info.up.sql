ALTER TABLE eureka_transfers ADD COLUMN recv_tx_gas_cost_usd NUMERIC;
ALTER TABLE eureka_transfers ADD COLUMN ack_tx_gas_cost_usd NUMERIC;
ALTER TABLE eureka_transfers ADD COLUMN timeout_tx_gas_cost_usd NUMERIC;

ALTER TABLE eureka_transfers ADD COLUMN recv_tx_relayer_address TEXT;
ALTER TABLE eureka_transfers ADD COLUMN ack_tx_relayer_address TEXT;
ALTER TABLE eureka_transfers ADD COLUMN timeout_tx_relayer_address TEXT;

ALTER TYPE eureka_relay_status ADD VALUE 'CALCULATING_RECV_TX_GAS_COST';
ALTER TYPE eureka_relay_status ADD VALUE 'CALCULATING_ACK_TX_GAS_COST';
ALTER TYPE eureka_relay_status ADD VALUE 'CALCULATING_TIMEOUT_TX_GAS_COST';
