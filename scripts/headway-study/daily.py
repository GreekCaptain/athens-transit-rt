# Fair schedule baseline: OASA daily schedule of 2026-11-03 (Tue, winter timetable) + GTFS stop
# offsets, as production builds timetable Arrivals on an expired GTFS. Weekday test days only.
from common import *
import json,re
st=pd.read_parquet('st.parquet'); tr=pd.read_csv('gtfs/trips.txt',dtype=str).set_index('trip_id')
pick=pd.read_csv('pick.csv',dtype=str); R=pd.read_parquet('eval.parquet')
STOPW={'ΝΥΧΤΕΡΙΝΗ','ΚΥΚΛΙΚΗ','ΠΡΟΣΩΡΙΝΗ','ΛΟΓΩ','ΕΡΓΩΝ','ΣΑΒΒΑΤΟ','ΚΥΡΙΑΚΗ','EXPRESS'}
def words(s): return set(re.findall(r'[Α-ΩΆ-Ώ]{3,}',s.upper()))-STOPW
def sec(t): h,m=t.split(':'); return int(h)*3600+int(m)*60
first=st.sort_values('stop_sequence').groupby('trip_id').arr.first()
dsched={}
for _,r in pick.iterrows():
    d=json.load(open(f'daily/{r.line}.json'))
    s=st[(st.route_short_name==r.line)&(st.stop_id==r.stop)].copy()
    s['start']=s.trip_id.map(first); s['off']=s.arr-s.start
    hs=tr.loc[s.trip_id,'trip_headsign'].iloc[0]; last=hs.split(' - ')[-1]
    dirs=d['directions']
    dd=dirs[0] if len(dirs)==1 else max(dirs,key=lambda x:len(words(x['headsign'])&words(last)))
    starts=s.start.values; offs=s.off.values
    out=[]
    for dep in dd['departures']:
        t=sec(dep['t']); k=np.argmin(np.abs(starts-t)); out.append(t+offs[k])
    dsched[(r.line,r.stop)]=np.sort(out); print(r.line,r.stop,'->',dd['headsign'],len(out),'vs gtfs',len(s))
def nxt(row):
    s=dsched[(row.line,row.stop)]; k=np.searchsorted(s,row.q,side='right')
    return s[k] if k<len(s) and s[k]-row.q<=3*3600 else np.nan
W=R[(~R.live)&(R.dtype=='wd')].copy()
W['daily']=W.apply(nxt,axis=1)
for e in ['sched','daily','hist']: W['a_'+e]=(W[e]-W.truth).abs()/60
B=W.dropna(subset=['a_sched','a_daily','a_hist'])
cls={'040':'dense','608':'dense','550':'dense','Χ95':'dense','224':'medium','421':'medium','11':'medium','721':'medium','914':'sparse','309Β':'sparse','500':'night','790':'night'}
B['cls']=B.line.map(cls)
for c,g in [('ALL',B)]+list(B.groupby('cls')):
    print(c.ljust(6),len(g),'  '.join(f"{e}: med {g['a_'+e].median():.1f} p90 {g['a_'+e].quantile(.9):.1f} ≤5′ {(g['a_'+e]<=5).mean():.0%}" for e in ['sched','daily','hist']))
print('hist better than daily by >1 min:',f"{(B.a_hist<B.a_daily-1).mean():.0%}",' worse:',f"{(B.a_hist>B.a_daily+1).mean():.0%}")
B=B.assign(sb=pd.cut(B.spread/60,[-1,3,6,999],labels=['≤3','3-6','>6']))
print(B.groupby('sb',observed=True).apply(lambda g:pd.Series({'share':len(g)/len(B),'hist_med':g.a_hist.median(),'hist≤5':(g.a_hist<=5).mean(),'daily_med':g.a_daily.median(),'daily≤5':(g.a_daily<=5).mean()})).round(2).to_string())
print(B.groupby('line').apply(lambda g:pd.Series({'n':len(g),'daily':g.a_daily.median(),'hist':g.a_hist.median(),'daily≤5':(g.a_daily<=5).mean(),'hist≤5':(g.a_hist<=5).mean()})).round(2).to_string())
