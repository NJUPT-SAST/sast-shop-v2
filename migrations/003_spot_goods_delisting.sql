BEGIN;

ALTER TABLE spot.spot_goods
    DROP CONSTRAINT spot_goods_stock_total_check,
    ADD CONSTRAINT spot_goods_stock_total_check CHECK (stock_total >= -1);

COMMENT ON COLUMN spot.spot_goods.stock_total IS '当前库存总量：-1 为已下架，0 为售罄，正数为可购买数量';

COMMIT;
