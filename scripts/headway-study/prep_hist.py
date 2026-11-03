import pandas as pd, glob
LINES=['040','608','550','224','Χ95','11','421','721','309Β','914','500','790']
parts=[];glob_counts=[]
for f in sorted(glob.glob('hist/*.csv*')):
    try: d=pd.read_csv(f,dtype={'line':str,'route_code':str,'veh':str,'gtfs':str,'trip_id':str,'shape_id':str})
    except Exception as e: print('bad',f,e); continue
    d["fix_t"]=pd.to_numeric(d.fix_t,errors="coerce"); d["s_m"]=pd.to_numeric(d.s_m,errors="coerce"); d=d.dropna(subset=["fix_t"]); d["fix_t"]=d.fix_t.astype("int64")
    glob_counts.append(d[['fix_t']])
    parts.append(d[d.line.isin(LINES)])
h=pd.concat(parts); h.to_parquet('hist.parquet')
g=pd.concat(glob_counts); g.to_parquet('allfix_t.parquet')
print(len(h),len(g)); print(h.gtfs.value_counts()); print((h.s_m<0).mean())
