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
N=N.copy()
print('## C0. coverage, no live bus: queries',len(N),' censored (no tracked pass within 3 h)',f"{N.truth.isna().mean():.1%}")
for e in ['sched','corr','hist','gap']:
    print(f"   {e}: predicts in {N[e].notna().mean():.0%} of no-live queries; predicts although censored: {(N[e].notna()&N.truth.isna()).sum()}")
print('   training days with no pass within 3 h (not outage), share of training-day lookups:',f"{N.nnone.sum()/(N.nnone.sum()+N.nx.sum()):.1%}")
U=N.dropna(subset=['truth'])
B=U.dropna(subset=['a_sched','a_hist','a_corr'])
print('## C. same cohort: no live bus, S, C and H all predict, n=',len(B))
def line(g,e): a=g['a_'+e]; return f"{e}: med {a.median():.1f} p75 {a.quantile(.75):.1f} p90 {a.quantile(.9):.1f} ≤5′ {(a<=5).mean():.0%}"
for c,g in [('ALL',B)]+list(B.groupby('cls')):
    print(' ',c,len(g)); [print('    ',line(g,e)) for e in ['sched','corr','hist']]
print('  H closer than S by >1 min:',f"{(B.a_hist<B.a_sched-1).mean():.0%}",' farther:',f"{(B.a_hist>B.a_sched+1).mean():.0%}")
G=U.dropna(subset=['a_gap','a_hist','a_sched'])
print('## C2. G paired with H and S (queries where all three predict), n=',len(G))
[print('    ',line(G,e)) for e in ['sched','hist','gap']]
print('## D. calibration with the proposed gate (days>=3), same cohort as C')
Bg=B[B.nx>=3].copy(); print('  gate days>=3 keeps',f"{len(Bg)/len(B):.0%}")
Bg['sb']=pd.cut(Bg.spread/60,[-1,3,6,10,999],labels=['≤3','3-6','6-10','>10'])
print(Bg.groupby('sb',observed=True).apply(lambda g:pd.Series({'share_of_cohort':len(g)/len(B),'H_med':g.a_hist.median(),'H_p90':g.a_hist.quantile(.9),'H≤5':(g.a_hist<=5).mean(),'S_med':g.a_sched.median(),'S_p90':g.a_sched.quantile(.9),'S≤5':(g.a_sched<=5).mean()})).round(2).to_string())
r=B[B.nx<3]; print('  gate fails (days<3): share',f"{len(r)/len(B):.0%}")
for dt,g in Bg.groupby('dtype'):
    t=g[g.spread<=180]; allnl=N[(N.dtype==dt)&(N.nx>=3)&(N.spread<=180)]
    print(f"  {dt}: rule (days>=3, IQR<=3′) fires on {len(t)/max(1,len(B[B.dtype==dt])):.0%} of cohort, {len(allnl)/max(1,len(N[N.dtype==dt])):.0%} of ALL no-live queries (incl. censored), of which censored {allnl.truth.isna().mean():.0%}; H med {t.a_hist.median():.1f} p90 {t.a_hist.quantile(.9):.1f} ≤5′ {(t.a_hist<=5).mean():.0%}; S med {t.a_sched.median():.1f}")
print('## E. per line (cohort C)')
print(B.groupby('line').apply(lambda g:pd.Series({'n':len(g),'sched':g.a_sched.median(),'hist':g.a_hist.median(),'sched≤5':(g.a_sched<=5).mean(),'hist≤5':(g.a_hist<=5).mean()})).round(2).to_string())
print('## F. outcome-conditioned: share of cohort where truth came >20 min after both S and H', f"{(((B.truth-B.sched)>1200)&((B.truth-B['hist'])>1200)).mean():.0%}")
