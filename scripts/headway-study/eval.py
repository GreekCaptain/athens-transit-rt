from common import *
import sys
pick=pd.read_csv('pick.csv',dtype=str)
P=pd.read_parquet('passes.parquet'); st=pd.read_parquet('st.parquet')
# --- outage mask: minutes with no fix at all, network-wide
g=pd.read_parquet('allfix_t.parquet').fix_t.values
mins=np.unique(g//60); t0,t1=mins.min(),mins.max()
have=np.zeros(t1-t0+1,bool); have[mins-t0]=True
dead=~have; 
# a minute is "out" if it is inside a run of >=5 dead minutes
out=np.zeros_like(dead); i=0;n=len(dead)
while i<n:
    if dead[i]:
        j=i
        while j<n and dead[j]: j+=1
        if j-i>=5: out[i:j]=True
        i=j
    else: i+=1
cum_out=np.concatenate([[0],np.cumsum(out)])
def has_outage(a,b):  # unix s
    if a//60<t0 or b//60>t1: return True
    ia=a//60-t0; ib=b//60-t0+1
    return cum_out[ib]-cum_out[ia]>0
print('outage minutes:',out.sum(), 'data from',datetime.fromtimestamp(t0*60,TZ),'to',datetime.fromtimestamp(t1*60,TZ))
HMAX=3*3600; STEP=300
rows=[]; hw=[]
for _,pk in pick.iterrows():
    L,stop=pk.line,pk.stop
    pp=P[(P.line==L)&(P.stop==stop)].sort_values('t')
    pt=pp.t.values; pfirst=pp.run_first.values
    # passes per service day as seconds since that day's local midnight
    sd={}
    for d in DAYS:
        m=midnight(d); a=m+4*3600; b=a+86400
        k=(pt>=a)&(pt<b); sd[d]=(pt[k]-m)
    # schedule per GTFS service, seconds since midnight (sorted)
    ss=st[(st.route_short_name==L)&(st.stop_id==stop)]
    sched={s:np.sort(g.arr.values) for s,g in ss.groupby('service_id')}
    # ----- headway table rows (observed vs schedule)
    for d in DAYS:
        x=sd[d]; s=sched.get(gtfs_service(d),np.array([]))
        for hr in range(4,28):
            lo,hi=hr*3600,(hr+1)*3600
            if has_outage(midnight(d)+lo,midnight(d)+hi): continue
            ox=x[(x>=lo)&(x<hi)]; sx=s[(s>=lo)&(s<hi)]
            hw.append((L,stop,d,dtype(d),week(d),hr,len(ox),len(sx)))
    # ----- predictions
    for d in DAYS:
        m=midnight(d); w=week(d); dt=dtype(d)
        train=[d2 for d2 in DAYS if dtype(d2)==dt and week(d2)!=w]
        s=sched.get(gtfs_service(d),np.array([]))
        # learned per scheduled arrival: offset and hit rate (from training days)
        corr=[]
        for sv in s:
            offs=[];hits=0
            # matching window: half the scheduled gap to neighbours, capped 10 min
            idx=np.searchsorted(s,sv)
            gap=min([abs(sv-s[j]) for j in (idx-1,idx+1) if 0<=j<len(s) and s[j]!=sv]+[1200])
            win=min(600,max(120,gap/2))
            for d2 in train:
                x=sd[d2]
                if len(x)==0: continue
                j=np.searchsorted(x,sv); c=[x[k] for k in (j-1,j) if 0<=k<len(x)]
                c=[v for v in c if abs(v-sv)<=win]
                if c: hits+=1; offs.append(min(c,key=lambda v:abs(v-sv))-sv)
            ntr=sum(1 for d2 in train if len(sd[d2])>0)
            corr.append((sv+(np.median(offs) if offs else 0), hits/ntr if ntr else 1))
        corr=sorted(corr)
        x=sd[d]
        for q in range(4*3600, 28*3600, STEP):
            T=m+q
            j=np.searchsorted(pt,T,side='right')
            if j>=len(pt) or pt[j]-T>HMAX: continue
            truth=pt[j]
            if has_outage(T,truth): continue
            live=pfirst[j]<=T   # the bus that comes next was already on the road
            # 1 schedule only
            k=np.searchsorted(s,q,side='right'); p_s=s[k] if k<len(s) and s[k]-q<=HMAX else np.nan
            # 2 schedule corrected (offset + skip trips that ran < 50% of training days)
            p_c=next((v for v,r in corr if v>q and r>=0.5 and v-q<=HMAX),np.nan)
            # 3 history: median over training days of 'next pass after q'
            nx=[]
            for d2 in train:
                y=sd[d2]; kk=np.searchsorted(y,q,side='right')
                if len(y) and kk<len(y) and y[kk]-q<=HMAX: nx.append(y[kk])
            p_h=np.median(nx) if len(nx)>=2 else np.nan
            sp_h=(np.percentile(nx,75)-np.percentile(nx,25)) if len(nx)>=2 else np.nan
            ntr=len(train)
            # 4 headway fallback: q + median observed gap in this hour (training) / 2
            gaps=[]
            for d2 in train:
                y=sd[d2]; yy=y[(y>=q-1800)&(y<q+1800)]
                if len(yy)>=2: gaps+=list(np.diff(yy))
            p_g=q+np.median(gaps)/2 if gaps else np.nan
            rows.append((L,stop,d,dt,w,q//3600,live,truth-m,p_s,p_c,p_h,p_g,sp_h,len(nx),ntr,q))
R=pd.DataFrame(rows,columns=['line','stop','day','dtype','week','hour','live','truth','sched','corr','hist','gap','spread','nx','ntr','q'])
R.to_parquet('eval.parquet'); pd.DataFrame(hw,columns=['line','stop','day','dtype','week','hour','obs','sched']).to_parquet('hw.parquet')
print(len(R), R.live.mean())
