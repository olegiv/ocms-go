-- Copyright (c) 2025-2026 Oleg Ivanchenko
-- SPDX-License-Identifier: GPL-3.0-or-later
--
-- Production-to-dev SQLite policy. An empty column classifies a whole table.
-- Keep entries record deliberate retention and do not generate statements.
WITH policy(table_name, column_name, action, value) AS (
    VALUES
        ('sessions', '', 'delete', NULL),
        ('oauth_tokens', '', 'delete', NULL),
        ('oauth_codes', '', 'delete', NULL),
        ('preview_links', '', 'delete', NULL),
        ('webhooks', 'secret', 'set', ''),
        ('webhooks', 'headers', 'set', '{}'),
        ('webhooks', 'url', 'set', ''),
        ('webhooks', 'is_active', 'set', 0),
        ('scheduled_tasks', 'url', 'set', ''),
        ('scheduled_tasks', 'is_active', 'set', 0),
        ('hcaptcha_settings', 'secret_key', 'set', ''),
        ('hcaptcha_settings', 'enabled', 'set', 0),
        ('embed_settings', 'settings', 'set', '{}'),
        ('embed_settings', 'is_enabled', 'set', 0),
        ('users', 'password_hash', 'keep', NULL),
        ('api_keys', 'key_hash', 'keep', NULL),
        ('api_keys', 'key_lookup', 'keep', NULL),
        ('user_totp', 'secret_enc', 'keep', NULL),
        ('user_recovery_codes', 'code_hash', 'keep', NULL),
        ('config', 'value', 'keep', NULL),
        ('config_translations', 'value', 'keep', NULL),
        ('form_fields', 'options', 'keep', NULL),
        ('widgets', 'settings', 'keep', NULL),
        ('migrator_import_jobs', 'options', 'keep', NULL),
        ('events', 'metadata', 'keep', NULL),
        ('events', 'request_url', 'keep', NULL),
        ('webhook_deliveries', 'payload', 'keep', NULL),
        ('webhook_deliveries', 'response_body', 'keep', NULL),
        ('webhook_deliveries', 'error_message', 'keep', NULL),
        ('scheduled_task_runs', 'response_body', 'keep', NULL),
        ('scheduled_task_runs', 'error_message', 'keep', NULL),
        ('dbmanager_query_history', 'query', 'keep', NULL),
        ('dbmanager_query_history', 'error', 'keep', NULL)
)
-- Emit statements for the installed schema only.
SELECT CASE action
    WHEN 'delete' THEN printf('DELETE FROM "%w";', table_name)
    WHEN 'set' THEN printf('UPDATE "%w" SET "%w" = %s;', table_name, column_name, quote(value))
END
FROM policy
WHERE action <> 'keep'
AND EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = policy.table_name)
AND (column_name = '' OR EXISTS (
    SELECT 1 FROM pragma_table_info(policy.table_name) WHERE name = policy.column_name
));
