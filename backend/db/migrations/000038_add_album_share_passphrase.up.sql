-- Synology albums that are shared WITH the logged-in user (rather than owned
-- by them) are reached through a passphrase that SYNO.Foto.Sharing.Misc hands
-- out alongside the album. Empty for owned albums and for every other source.
ALTER TABLE albums ADD COLUMN share_passphrase TEXT NOT NULL DEFAULT '';
