-- v0.3 : groupes, tags et profil réseau (miroir de la configuration)
ALTER TABLE apis ADD COLUMN group_id TEXT;
ALTER TABLE apis ADD COLUMN tags TEXT NOT NULL DEFAULT '[]';   -- tableau JSON
ALTER TABLE apis ADD COLUMN network_profile TEXT NOT NULL DEFAULT 'default';
CREATE INDEX idx_apis_group ON apis(group_id);
