ALTER TYPE eureka_relay_status ADD VALUE 'COMPLETE_WITH_WRITE_ACK_SUCCESS';

CREATE TYPE eureka_write_ack_status AS ENUM (
    'SUCCESS',
    'ERROR',
    'UNKNOWN'
);

ALTER TABLE eureka_transfers ADD COLUMN write_ack_status eureka_write_ack_status;
