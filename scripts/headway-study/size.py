# Measure the size of the proposed table: per (line, stop, day type) the distinct next(q) values
# on a 5-min grid (one row = uint16 minute of service day + uint8 spread min + uint8 days = 4 B),
# compared with the GTFS scheduled passes of the same stops, to extrapolate to the network.
from common import *
R=pd.read_parquet('eval.parquet'); st=pd.read_parquet('st.parquet')
rows=0; sched=0
for (L,stop,dt),g in R.dropna(subset=['hist']).groupby(['line','stop','dtype']):
    rows+=g['hist'].round(-1).nunique()   # distinct next(q) per cell (10 s rounding)
    svc={'wd':'day_3','sat':'day_6','sun':'day_7'}[dt]
    sched+=((st.route_short_name==L)&(st.stop_id==stop)&(st.service_id==svc)).sum()
print('study: distinct rows',rows,' scheduled passes (one day per type)',sched,' ratio',round(rows/sched,2))
net=656767+548515+406352
print('network estimate: rows ≈',int(net*rows/sched),' bytes ≈',f"{net*rows/sched*4/1e6:.1f} MB",'+ hourly headways 18,007 pairs × 3 × 24 × 2 B ≈',f"{18007*3*24*2/1e6:.1f} MB")
