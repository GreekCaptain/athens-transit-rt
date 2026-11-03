import pandas as pd, numpy as np
from datetime import datetime, timedelta, date
from zoneinfo import ZoneInfo
TZ=ZoneInfo('Europe/Athens')
HOLIDAYS={date(2026,10,28)}   # OXI day: OASA runs the Sunday timetable
DAYS=[date(2026,10,6)+timedelta(d) for d in range(23)]  # 10-06 .. 10-28 (10-29 has ~no data)
def dtype(d): 
    if d in HOLIDAYS or d.weekday()==6: return 'sun'
    return 'sat' if d.weekday()==5 else 'wd'
def gtfs_service(d): return 'day_7' if d in HOLIDAYS else f'day_{d.isoweekday()}'
def week(d): return (d-date(2026,10,5)).days//7   # 0: 10-05..10-11 ...
def midnight(d): return int(datetime(d.year,d.month,d.day,tzinfo=TZ).timestamp())
def service_day(ts):   # service day runs 04:00 -> 04:00
    dt=datetime.fromtimestamp(ts,TZ); d=dt.date()
    return d-timedelta(1) if dt.hour<4 else d
