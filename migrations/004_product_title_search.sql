BEGIN;

CREATE EXTENSION IF NOT EXISTS zhparser WITH SCHEMA public;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_catalog.pg_ts_config
        WHERE cfgname = 'product_zh'
          AND cfgnamespace = 'catalog'::regnamespace
    ) THEN
        EXECUTE 'CREATE TEXT SEARCH CONFIGURATION catalog.product_zh (PARSER = public.zhparser)';
    END IF;
END;
$$;

-- 2：配置 n,v,a,i,e,l 到 pg_catalog.simple 的映射
-- 重建映射，使脚本可重复执行
ALTER TEXT SEARCH CONFIGURATION catalog.product_zh
    DROP MAPPING IF EXISTS FOR n, v, a, i, e, l;

ALTER TEXT SEARCH CONFIGURATION catalog.product_zh
    ADD MAPPING FOR n, v, a, i, e, l WITH pg_catalog.simple;

-- 3：创建 GIN 索引，沿用原索引名，兼容已有索引
CREATE INDEX IF NOT EXISTS idx_catalog_product_template_title_fts
ON catalog.catalog_product_template
USING GIN (
    to_tsvector('catalog.product_zh'::regconfig, title)
);

COMMIT;
