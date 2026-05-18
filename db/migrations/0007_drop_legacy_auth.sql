-- Remove embedded IAM; fleet uses iag-authentication exclusively.
-- Run after 0005 (notifications no longer reference users.id).

DROP TABLE IF EXISTS user_user_permissions CASCADE;
DROP TABLE IF EXISTS group_permissions CASCADE;
DROP TABLE IF EXISTS user_groups CASCADE;
DROP TABLE IF EXISTS auth_tokens CASCADE;
DROP TABLE IF EXISTS auth_sessions CASCADE;
DROP TABLE IF EXISTS auth_permissions CASCADE;
DROP TABLE IF EXISTS auth_groups CASCADE;
DROP TABLE IF EXISTS users CASCADE;
