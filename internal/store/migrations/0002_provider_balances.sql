-- Forward-only; never edit an applied file.
--
-- provider_accounts is global but balance_snapshots is per-user. This table
-- holds the canonical "current balance" per provider and currency; snapshots
-- remain the per-user history and external-import trail.

CREATE TABLE IF NOT EXISTS provider_balances (
  provider_id TEXT NOT NULL,
  currency TEXT NOT NULL,
  total_balance REAL NOT NULL,
  granted_balance REAL NOT NULL,
  topped_up_balance REAL NOT NULL,
  is_available INTEGER NOT NULL,
  low_balance INTEGER NOT NULL,
  source TEXT NOT NULL,
  fetched_at TEXT NOT NULL,
  PRIMARY KEY(provider_id, currency)
);
CREATE INDEX IF NOT EXISTS ix_provider_balances_fetched ON provider_balances(fetched_at);

INSERT OR REPLACE INTO provider_balances
  (provider_id, currency, total_balance, granted_balance, topped_up_balance,
   is_available, low_balance, source, fetched_at)
SELECT s.provider_id, s.currency, s.total_balance, s.granted_balance,
       s.topped_up_balance, s.is_available, s.low_balance, s.source, s.fetched_at
  FROM balance_snapshots s
  JOIN (
        SELECT provider_id, currency, MAX(fetched_at) AS latest
          FROM balance_snapshots
         GROUP BY provider_id, currency
       ) m
    ON m.provider_id = s.provider_id
   AND m.currency = s.currency
   AND m.latest = s.fetched_at;