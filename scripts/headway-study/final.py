from common import *
R=pd.read_parquet('eval.parquet'); H=pd.read_parquet('hw.parquet')
cls={'040':'dense','608':'dense','550':'dense','Χ95':'dense','224':'medium','421':'medium','11':'medium','721':'medium','914':'sparse','309Β':'sparse','500':'night','790':'night'}
R['cls']=R.line.map(cls); H['cls']=H.line.map(cls)
for e in ['sched','corr','hist','gap']: R['a_'+e]=(R[e]-R.truth).abs()/60
N=R[~R.live]
print('## A. no-live share of queries by class'); print(R.groupby('cls').live.apply(lambda x:1-x.mean()).round(2).to_dict())
print('## B. stability: weekday hourly pass count, per line (cells with sched>0 or obs>0)')
W=H[(H.hour>=6)&(H.hour<22)]
rows=[]
for L,g in W.groupby('line'):
    for dt,gg in g.groupby('dtype'):
        c=gg.groupby(['stop','hour']).obs.agg(['mean','std','count']); c=c[c['count']>=2]
        wk=gg.groupby(['stop','hour','week']).obs.mean().unstack()
        dif=[]
        for a in wk.columns:
            for b in wk.columns:
                if a<b: dif+=list((wk[a]-wk[b]).abs().dropna())
        sc=gg.groupby(['stop','hour']).sched.mean()
        rows.append((L,dt,round(c['mean'].mean(),1),round((c['std']/c['mean'].clip(lower=.5)).median(),2),round(np.mean(dif),2) if dif else None, round((c['mean']/sc.reindex(c.index).replace(0,np.nan)).median(),2)))
S=pd.DataFrame(rows,columns=['line','dtype','passes/h','CV day-to-day','|Δ| week-to-week (passes/h)','obs/sched'])
print(S.to_string(index=False))
# observed headway table example for 2 lines
print('## B2. headway table sample (weekday): median gap, IQR, n gaps vs scheduled median gap')
P=pd.read_parquet('passes.parquet'); st=pd.read_parquet('st.parquet'); pick=pd.read_csv('pick.csv',dtype=str)
for L in ['040','224','914','790']:
    stop=pick[pick.line==L].stop.iloc[0]; p=P[(P.line==L)&(P.stop==stop)].copy()
    p['d']=p.t.map(service_day); p=p[p.d.map(lambda d:d in DAYS and dtype(d)=='wd')]
    p['tod']=p.t-p.d.map(midnight); p=p.sort_values('t'); p['gap']=p.groupby('d').tod.diff()/60; p['h']=p.tod//3600
    ss=st[(st.route_short_name==L)&(st.stop_id==stop)&(st.service_id=='day_3')].arr.sort_values(); sg=pd.Series(np.diff(ss.values)/60,index=(ss.values[1:]//3600))
    t=p.groupby('h').gap.agg(med='median',q1=lambda x:x.quantile(.25),q3=lambda x:x.quantile(.75),n='count').join(sg.groupby(level=0).median().rename('sched_med'))
    print(L,stop); print(t.loc[[h for h in [6,8,12,17,21,23,25] if h in t.index]].round(0).to_string())
print('## C. honest error, no live bus, queries where sched AND hist both predict')
B=N.dropna(subset=['a_sched','a_hist'])
def line(g,e): a=g['a_'+e].dropna(); return f"{e}: n {len(a)} med {a.median():.1f} p75 {a.quantile(.75):.1f} p90 {a.quantile(.9):.1f} ≤5′ {(a<=5).mean():.0%}"
for c,g in [('ALL',B)]+list(B.groupby('cls')):
    print(' ',c); [print('    ',line(g,e)) for e in ['sched','corr','hist','gap']]
print('  hist strictly better than sched:',f"{(B.a_hist<B.a_sched-1).mean():.0%}",' worse:',f"{(B.a_hist>B.a_sched+1).mean():.0%}")
print('## D. calibration: hist error by spread of training-day next passes (IQR)')
B=B.assign(sb=pd.cut(B.spread/60,[-1,3,6,10,20,999],labels=['≤3','3-6','6-10','10-20','>20']))
print(B.groupby('sb',observed=True).apply(lambda g:pd.Series({'share':len(g)/len(B),'med':g.a_hist.median(),'p90':g.a_hist.quantile(.9),'≤5′':(g.a_hist<=5).mean(),'sched_med':g.a_sched.median()})).round(2).to_string())
print('## E. per line (no live, sched∩hist)')
print(B.groupby('line').apply(lambda g:pd.Series({'n':len(g),'sched':g.a_sched.median(),'hist':g.a_hist.median(),'sched≤5':(g.a_sched<=5).mean(),'hist≤5':(g.a_hist<=5).mean()})).round(2).to_string())
print('## F. missing bus share (truth > 20 min after both)', f"{(((B.truth-B.sched)>1200)&((B.truth-B['hist'])>1200)).mean():.0%}")
