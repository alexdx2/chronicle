-- CREATE STREAM old (a INT) WITH (KAFKA_TOPIC='k-commented');
CREATE STREAM raw (id VARCHAR KEY) WITH (KAFKA_TOPIC='k-raw', VALUE_FORMAT='JSON');
CREATE OR REPLACE TABLE users (id VARCHAR PRIMARY KEY) WITH (
  kafka_topic = 'k-users', value_format = 'JSON');
/* CREATE STREAM x WITH (KAFKA_TOPIC='k-block'); */
CREATE STREAM enriched WITH (KAFKA_TOPIC='k-enriched', PARTITIONS=1) AS
  SELECT r.id, 'AS SELECT in text' AS note FROM raw r EMIT CHANGES;
CREATE TABLE counts AS SELECT id, COUNT(*) FROM raw GROUP BY id;
INSERT INTO raw (id) VALUES ('KAFKA_TOPIC=''k-insert''');
SET 'x' = 'KAFKA_TOPIC=''k-set''';
