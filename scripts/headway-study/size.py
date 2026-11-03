# Size of the proposed table, fitted once on ALL days (as production would), per
# (line, stop, day type) on a 5-min grid. Consecutive grid points with the same
# (next, spread, days) merge into one row: start minute uint16 + next minute uint16 +
# spread min uint8 + days uint8 = 6 B. Extrapolated by scheduled stop passes.
from common import *
P=pd.read_parquet('passes.parquet'); st=pd.read_parquet('st.parquet'); pick=pd.read_csv('pick.csv',dtype=str)
HMAX=3*3600; rows=0; sched=0
for _,r in pick.iterrows():
    pt=np.sort(P[(P.line==r.line)&(P.stop==r.stop)].t.values)
    sd={d:np.sort([wall(v,d) for v in pt[(pt>=at(d,4*3600))&(pt<at(d,28*3600)+HMAX)]]) for d in DAYS}
    for dt in ['wd','sat','sun']:
        days=[d for d in DAYS if dtype(d)==dt]; prev=None
        for q in range(4*3600,28*3600,300):
            nx=[]
            for d in days:
                y=sd[d]; k=np.searchsorted(y,q,side='right')
                if k<len(y) and y[k]-q<=HMAX: nx.append(y[k])
            key=None if len(nx)<2 else (int(np.median(nx)//60), int(min(255,(np.percentile(nx,75)-np.percentile(nx,25))//60)), len(nx))
            if key is not None and key!=prev: rows+=1
            prev=key
        svc={'wd':'day_3','sat':'day_6','sun':'day_7'}[dt]
        sched+=((st.route_short_name==r.line)&(st.stop_id==r.stop)&(st.service_id==svc)).sum()
net=656767+548515+406352
print('single fit: rows',rows,' scheduled passes',sched,' rows/pass',round(rows/sched,2))
print('network: rows ≈',int(net*rows/sched),' at 6 B ≈',f"{net*rows/sched*6/1e6:.1f} MB",' + headways ≈',f"{18007*3*24*2/1e6:.1f} MB")
