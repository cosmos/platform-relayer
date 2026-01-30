--- cant actually delete values from an enum so we do nothing
DROP TYPE IF EXISTS eureka_write_ack_status;
ALTER TABLE eureka_transfers DROP COLUMN eureka_write_ack_status;
