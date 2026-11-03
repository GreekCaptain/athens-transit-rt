from common import *
P=pd.read_parquet('passes.parquet'); sp=pd.read_parquet('stoppos.parquet')
st=pd.read_parquet('st.parquet')
dirs=st.drop_duplicates('shape_id').set_index('shape_id').direction_id
sp['dir']=sp['shape'].map(dirs); sp['kmax']=sp.groupby('shape').k.transform('max')
mid=sp[(sp.k>=0.3*sp.kmax)&(sp.k<=0.6*sp.kmax)]
cnt=P.groupby(['line','stop']).size().rename('n').reset_index()
cnt=cnt.merge(mid[['stop','dir','shape']].drop_duplicates(['stop']),on='stop')
cnt['shape_line']=cnt['shape'].map(st.drop_duplicates('shape_id').set_index('shape_id').route_short_name)
cnt=cnt[cnt.line==cnt.shape_line]
pick=cnt.sort_values('n',ascending=False).drop_duplicates(['line','shape']).groupby('line').head(2)
names=pd.read_csv('gtfs/stops.txt',dtype={'stop_id':str}).set_index('stop_id').stop_name
pick['name']=pick.stop.map(names)
print(pick[['line','dir','stop','name','n']].sort_values(['line','dir']).to_string())
pick[['line','dir','stop','name']].to_csv('pick.csv',index=False)
