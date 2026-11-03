from common import *
R=pd.read_parquet('eval.parquet')
cls={'040':'dense','608':'dense','550':'dense','Χ95':'dense','224':'medium','421':'medium','11':'medium','721':'medium','914':'sparse','309Β':'sparse','500':'night','790':'night'}
R['cls']=R.line.map(cls); N=R[~R.live].dropna(subset=['truth']).copy()
for e in ['sched','corr','hist','gap']: N['a_'+e]=(N[e]-N.truth).abs()/60
# "missing": truth arrives > 20 min after BOTH sched and hist said (the predicted bus never showed up / was not tracked)
N['late20']=((N.truth-N.sched)>1200)&((N.truth-N['hist'])>1200)
print('OUTCOME-CONDITIONED (not causal): share of no-live queries where the next tracked pass came >20 min after both sched and hist:',f"{N.late20.mean():.0%}")
print(N.groupby('cls').late20.mean().round(2).to_dict())
M=N[~N.late20].dropna(subset=['a_sched','a_hist','a_corr'])
print('--- excluding those queries (selected on the outcome, so optimistic), n=',len(M))
for c,g in [('all',M)]+list(M.groupby('cls')):
    print(' ',c.ljust(6),len(g),'  '.join(f"{e}: med {g['a_'+e].median():.1f} p90 {g['a_'+e].quantile(.9):.1f} ≤5′ {(g['a_'+e]<=5).mean():.0%}" for e in ['sched','corr','hist']))
print('hist beats sched (strictly) share:',f"{(M.a_hist<M.a_sched).mean():.0%}", ' ties(<=1 min diff):',f"{((M.a_hist-M.a_sched).abs()<=1).mean():.0%}")
# by hour band
M['band']=pd.cut(M.hour,[3,6,10,16,20,24,28],labels=['04-06','06-10','10-16','16-20','20-24','00-04'])
print(M.groupby('band',observed=True).apply(lambda g:pd.Series({'n':len(g),'sched':g.a_sched.median(),'hist':g.a_hist.median()})).round(1).to_string())
