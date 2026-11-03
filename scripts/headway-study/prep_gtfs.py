import pandas as pd
g='gtfs/'
st=pd.read_csv(g+'stop_times.txt',dtype={'trip_id':str,'stop_id':str},usecols=['trip_id','arrival_time','stop_id','stop_sequence'])
tr=pd.read_csv(g+'trips.txt',dtype=str)
ro=pd.read_csv(g+'routes.txt',dtype=str)
tr=tr.merge(ro[['route_id','route_short_name']],on='route_id')
def sec(s):
    h,m,x=s.split(':');return int(h)*3600+int(m)*60+int(x)
st['arr']=st.arrival_time.map(sec)
st=st.drop(columns='arrival_time').merge(tr[['trip_id','service_id','shape_id','direction_id','route_short_name']],on='trip_id')
st.to_parquet('st.parquet')
first=st.sort_values('stop_sequence').groupby('trip_id').first()
night=first[(first.arr%86400>=3600*1)&(first.arr%86400<3600*4.5)]
print(night.route_short_name.value_counts().head(20))
