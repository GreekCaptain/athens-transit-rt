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
        a=at(d,4*3600); b=at(d,28*3600)
        k=(pt>=a)&(pt<b); sd[d]=np.array([wall(v,d) for v in pt[k]])
    # schedule per GTFS service, seconds since midnight (sorted)
    ss=st[(st.route_short_name==L)&(st.stop_id==stop)]
    sched={s:np.sort(g.arr.values) for s,g in ss.groupby('service_id')}
    # ----- headway table rows (observed vs schedule)
    for d in DAYS:
        x=sd[d]; s=sched.get(gtfs_service(d),np.array([]))
        for hr in range(4,28):
            lo,hi=hr*3600,(hr+1)*3600
            if has_outage(at(d,lo),at(d,hi)): continue
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
                if len(x)==0 or has_outage(at(d2,sv-win),at(d2,sv+win)): continue
                j=np.searchsorted(x,sv); c=[x[k] for k in (j-1,j) if 0<=k<len(x)]
                c=[v for v in c if abs(v-sv)<=win]
                if c: hits+=1; offs.append(min(c,key=lambda v:abs(v-sv))-sv)
            ntr=sum(1 for d2 in train if len(sd[d2])>0 and not has_outage(at(d2,sv-win),at(d2,sv+win)))
            corr.append((sv+(np.median(offs) if offs else 0), hits/ntr if ntr else 1))
        corr=sorted(corr)
        x=sd[d]
        for q in range(4*3600, 28*3600, STEP):
            T=at(d,q)
            j=np.searchsorted(pt,T,side='right')
            if j>=len(pt) or pt[j]-T>HMAX:      # no tracked pass within 3 h: censored query
                if has_outage(T,T+HMAX): continue
                truth=np.nan; live=False
            else:
                if has_outage(T,pt[j]): continue
                truth=wall(pt[j],d)
                k2=np.searchsorted(pt,T+HMAX,side='right')
                live=bool((pfirst[j:k2]<=T).any())   # any bus already on a run that reaches the stop
            # 1 schedule only
            k=np.searchsorted(s,q,side='right'); p_s=s[k] if k<len(s) and s[k]-q<=HMAX else np.nan
            # 2 schedule corrected (offset + skip trips that ran < 50% of training days)
            p_c=next((v for v,r in corr if v>q and r>=0.5 and v-q<=HMAX),np.nan)
            # 3 history: median over training days of 'next pass after q'
            nx=[]; nnone=0
            for d2 in train:
                y=sd[d2]; kk=np.searchsorted(y,q,side='right')
                if len(y) and kk<len(y) and y[kk]-q<=HMAX:
                    if not has_outage(at(d2,q),at(d2,y[kk])): nx.append(y[kk])
                elif not has_outage(at(d2,q),at(d2,q+HMAX)): nnone+=1
            p_h=np.median(nx) if len(nx)>=2 else np.nan
            sp_h=(np.percentile(nx,75)-np.percentile(nx,25)) if len(nx)>=2 else np.nan
            ntr=len(train)
            # 4 headway fallback: q + median observed gap in this hour (training) / 2
            gaps=[]
            for d2 in train:
                if has_outage(at(d2,q-1800),at(d2,q+1800)): continue
                y=sd[d2]; yy=y[(y>=q-1800)&(y<q+1800)]
                if len(yy)>=2: gaps+=list(np.diff(yy))
            p_g=q+np.median(gaps)/2 if gaps else np.nan
            rows.append((L,stop,d,dt,w,q//3600,live,truth,p_s,p_c,p_h,p_g,sp_h,len(nx),ntr,q,nnone))
R=pd.DataFrame(rows,columns=['line','stop','day','dtype','week','hour','live','truth','sched','corr','hist','gap','spread','nx','ntr','q','nnone'])
R.to_parquet('eval.parquet'); pd.DataFrame(hw,columns=['line','stop','day','dtype','week','hour','obs','sched']).to_parquet('hw.parquet')
print(len(R), R.live.mean())
