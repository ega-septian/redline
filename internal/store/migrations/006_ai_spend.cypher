// Index untuk batas biaya AI harian: jumlahkan biaya sejak awal hari.
CREATE INDEX ai_spend_at IF NOT EXISTS FOR (s:AiSpend) ON (s.at);
