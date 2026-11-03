# Extract stop passes from the fix history: a pass = the time a run's s_m crosses the stop's
# position along its shape (linear interpolation between two fixes <= MAXDT apart).
import pandas as pd, numpy as np, math, json, sys
from datetime import datetime, timedelta, date
from zoneinfo import ZoneInfo
TZ=ZoneInfo('Europe/Athens'); R=6371000.0; C=math.cos(37.98*math.pi/180)
MAXDT=600   # max fix gap bridged by interpolation (s)
LINES=['040','608','550','224','Χ95','11','421','721','309Β','914','500','790']
st=pd.read_parquet('st.parquet'); st=st[st.route_short_name.isin(LINES)]
sh=pd.read_csv('gtfs/shapes.txt',dtype={'shape_id':str}).sort_values(['shape_id','shape_pt_sequence'])
stops=pd.read_csv('gtfs/stops.txt',dtype={'stop_id':str}).set_index('stop_id')
def xy(lat,lon): return np.array([lon*math.pi/180*R*C, lat*math.pi/180*R])
shapes={}
for sid,g in sh[sh.shape_id.isin(st.shape_id.unique())].groupby('shape_id'):
    P=np.stack([xy(a,b) for a,b in zip(g.shape_pt_lat,g.shape_pt_lon)])
    cum=np.concatenate([[0],np.cumsum(np.hypot(*np.diff(P,axis=0).T))])
    shapes[sid]=(P,cum)
def snap(P,cum,q,minalong):
    best=None
    for i in range(len(P)-1):
        a,b=P[i],P[i+1]; ab=b-a; L=ab@ab
        t=0 if L==0 else max(0,min(1,((q-a)@ab)/L))
        along=cum[i]+t*math.sqrt(L)
        if along<minalong-1: continue
        d=np.hypot(*(a+t*ab-q))
        if d<=150 and (best is None or d<best[0]-1e-9): best=(d,along)
        if best and along>best[1]+2000 and d>500: break   # first good pass, not a later loop
    return None if best is None else best[1]
# stop positions per shape (from the trip with most stops)
stoppos={}  # (shape, stop_id) -> along
for sid,g in st.groupby('shape_id'):
    tid=g.groupby('trip_id').size().idxmax(); seq=g[g.trip_id==tid].sort_values('stop_sequence')
    P,cum=shapes[sid]; m=0
    for k,s in enumerate(seq.stop_id):
        q=xy(stops.at[s,'stop_lat'],stops.at[s,'stop_lon']); a=snap(P,cum,q,m)
        if a is None: continue
        stoppos[(sid,s,k)]=a; m=a
h=pd.read_parquet('hist.parquet'); h=h[h.s_m>=0].sort_values(['veh','fix_t'])
out=[]
for (veh,sid),g in h.groupby(['veh','shape_id'],sort=False):
    if sid not in shapes: continue
    t=g.fix_t.values; s=g.s_m.values.astype(float); line=g.line.values
    # split into runs: time gap > 15 min or going back > 300 m
    brk=np.where((np.diff(t)>900)|(np.diff(s)< -300))[0]+1
    for run_i,(a,b) in enumerate(zip(np.r_[0,brk],np.r_[brk,len(t)])):
        if b-a<2: continue
        tt,ss=t[a:b],s[a:b]; first=tt[0]
        for (sid2,stop,k),A in stoppos.items():
            if sid2!=sid or k<2: continue        # skip first two stops (terminus dwell)
            if not (ss[0]<A<=ss[-1]): continue
            if not (ss[0]<=A-1500 or ss[0]<=200): continue   # jitter re-runs start near the stop
            j=np.argmax(ss>=A)
            if j==0: continue
            dt=tt[j]-tt[j-1]
            if dt>MAXDT: continue
            ds=ss[j]-ss[j-1]
            p=tt[j-1]+(A-ss[j-1])/ds*dt if ds>0 else tt[j]
            out.append((line[0],stop,sid,int(round(p)),veh,int(first),int(tt[-1])))
P=pd.DataFrame(out,columns=['line','stop','shape','t','veh','run_first','run_last'])
P=P.sort_values('t').drop_duplicates(['line','stop','veh','t'])
# dedupe: same veh, same stop, within 10 min -> keep first
# same bus, same stop: a real second pass needs a full round (>= 0.6 x trip duration of its shape);
# closer repeats are matcher jitter (seen on loop lines). Keep the first, iteratively.
td=st.groupby(['shape_id','trip_id']).arr.agg(lambda a:a.max()-a.min()).groupby('shape_id').median()
P['mingap']=P['shape'].map(td).fillna(3600)*0.6
keep=[]
for _,g in P.groupby(['line','stop','veh']):
    last=-1e18
    for i,t,mg in zip(g.index,g.t.values,g.mingap.values):
        if t-last>=max(mg,600): keep.append(i); last=t
P=P.loc[keep].drop(columns='mingap').sort_values('t')
P.to_parquet('passes.parquet'); print(len(P)); print(P.groupby('line').size())
pd.DataFrame([(a,b,k,v) for (a,b,k),v in stoppos.items()],columns=['shape','stop','k','along']).to_parquet('stoppos.parquet')
