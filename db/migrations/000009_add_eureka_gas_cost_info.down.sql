ALTER TABLE eureka_transfers DROP COLUMN recv_tx_gas_cost_usd;
ALTER TABLE eureka_transfers DROP COLUMN ack_tx_gas_cost_usd;
ALTER TABLE eureka_transfers DROP COLUMN timeout_tx_gas_cost_usd;

ALTER TABLE eureka_transfers DROP COLUMN recv_tx_relayer_address;
ALTER TABLE eureka_transfers DROP COLUMN ack_tx_relayer_address;
ALTER TABLE eureka_transfers DROP COLUMN timeout_tx_relayer_address;
