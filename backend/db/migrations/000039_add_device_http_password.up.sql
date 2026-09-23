-- Password for the frame's own HTTP API, when its owner has enabled the
-- optional authentication (esp32-photoframe #130). Empty = the frame is open,
-- which is the firmware default.
ALTER TABLE devices ADD COLUMN http_password TEXT NOT NULL DEFAULT '';
