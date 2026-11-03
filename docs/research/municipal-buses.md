# Municipal bus services of Attica: sources, quality, licence

Research for #59 (2026-11-03). No code. Question: can the map show the municipal buses
(δημοτική συγκοινωνία) next to OASA, and is a static «Δημοτικά» layer worth it?

## Summary

- About 37 Attica municipalities run a municipal bus (list of athenstransport.com, last
  updated 2023-03). Most run 1-3 lines with 4-17 trips per weekday. None of them is in the
  OASA GTFS or in OASA telematics.
- **No municipality publishes GTFS** or any other machine-readable feed. data.gov.gr and
  data.nap.gov.gr have no municipal bus dataset. The official source is a web page, an image
  or a PDF per municipality, without a licence statement («All rights reserved» footers).
- **OpenStreetMap is the only open, machine-readable source**: 37 route relations for 12
  municipalities (ODbL). Route geometry is there for all 37; stops for 27 of them; timetables
  for none.
- **Live GPS**: three municipalities have a vendor app (Μεταμόρφωση, Νέα Ιωνία, Νέα Σμύρνη).
  No public API, no open data. Moovit shows some municipal lines (Πειραιάς, Γλυφάδα, Νέα
  Σμύρνη, Άγιος Δημήτριος); Moovit data is proprietary.
- Δήμος Αθηναίων plans its own network («Athens Move»: 4 lines, 185 stops, 17 electric buses),
  reported to start in 2027.

## Table: municipality → source → quality → licence

Quality: **A** = machine-readable route + stops; **B** = machine-readable route only;
**C** = official page/PDF/image only (manual work, may be stale); **live** = the municipality
has a live-position app.

| Municipality | Lines | Source | Quality | Licence |
|---|---|---|---|---|
| Αμαρουσίου | 5 (010-080, shuttle) | OSM, 12 relations ([20002907](https://www.openstreetmap.org/relation/20002907)…20002919, edited 2025-12 to 2026-08); [maroussi.gr](http://www.maroussi.gr/default.aspx?lang=el-GR&page=136) timetable | A (some directions have only 4 stops) | ODbL (OSM); site: none |
| Χαλανδρίου | 4 (Β1, Β2, Γ, Ο) | OSM, 4 relations ([20462642](https://www.openstreetmap.org/relation/20462642) etc., edited 2026-10/11); [chalandri.gr](https://www.chalandri.gr/yphresies/ypodomes-erga/dimotiki-sygkoinonia/) | A for Β1/Β2, B for Γ, Ο | ODbL; site: none |
| Νέας Σμύρνης | 3 (Άνω, Κάτω, κυκλική) | OSM, 5 relations ([17502627](https://www.openstreetmap.org/relation/17502627) etc.); [PDF 2017](http://www.neasmyrni.gr/Formes/2017/dromastsygk2017.pdf); [telematics app](https://neasmyrni.gr/%CE%B7-%CF%84%CE%B7%CE%BB%CE%B5%CE%BC%CE%B1%CF%84%CE%B9%CE%BA%CE%B7-%CF%83%CF%84%CE%B7%CE%BD-%CF%85%CF%80%CE%B7%CF%81%CE%B5%CF%83%CE%B9%CE%B1-%CF%84%CE%B7%CF%83-%CE%B4%CE%B7%CE%BC%CE%BF%CF%84%CE%B9/), QR at stops | A + live | ODbL; app: none |
| Αγίου Δημητρίου | 2 (Α, Β) | OSM, 3 relations ([17464843](https://www.openstreetmap.org/relation/17464843) etc., 2024); [dad.gr](https://dad.gr/environment/perivallon/dimotiki-sygkoinonia/) | A (2024 data) | ODbL; site: none |
| Ελληνικού-Αργυρούπολης | 3 | OSM, 3 relations ([7145087](https://www.openstreetmap.org/relation/7145087) etc., 2024); [municipal page](http://www.elliniko-argyroupoli.gr/article.php?cat=180) | A (2024 data) | ODbL; site: none |
| Βύρωνα | 1 (+1 school) | OSM [20633754](https://www.openstreetmap.org/relation/20633754) (65 stops, 2026-09); [dimosbyrona.gr](https://www.dimosbyrona.gr/article.php?id=120) | A | ODbL; site: none |
| Μεταμόρφωσης | 1 (κυκλική) | OSM [20584868](https://www.openstreetmap.org/relation/20584868) (no stops); [app «Metamorfosi City Bus»](https://metamorfossi.gov.gr/katoikoi/metakinisi/dimotiki-sugkoinonia), web metamorfwsi.3pi-telematics.com | B + live | ODbL; app: «All rights reserved» |
| Νέας Ιωνίας | 2 | OSM, 2 relations (no stops); [app «nea ionia bus»](https://neaionia.gr/dimosieuseis/perase-sti-nea-epochi-i-dimotiki-sygkoinonia-tis-neas-ionias) (2025-12) | B + live | ODbL; app: none |
| Παπάγου-Χολαργού | 2-3 | OSM, 2 relations (no stops, 2026-05); [dpapxol.gov.gr](https://www.dpapxol.gov.gr/e-politis/2015-07-30-06-35-07/dimotiki-sigkoinwnia.html) | B | ODbL; site: none |
| Λυκόβρυσης-Πεύκης | 2 | OSM, 2 relations (no stops, 2026-04); [likovrisipefki.gr](https://likovrisipefki.gr/index.php/citizen/1/dromologia) | B | ODbL; site: none |
| Γαλατσίου | 1 | OSM [20603133](https://www.openstreetmap.org/relation/20603133) (no stops); [galatsi.gov.gr](https://www.galatsi.gov.gr/dimotiki-sygkinonia/) | B | ODbL; site: none |
| Ηρακλείου | 1-2 | OSM [20516010](https://www.openstreetmap.org/relation/20516010) (no stops); [iraklio.gr](https://www.iraklio.gr/el/dimotiki-sygkoinonia) stop list | B | ODbL; site: none |
| Πειραιά | 5 | Google Doc + Scribd maps (2018), Moovit | C (Moovit: proprietary) | none |
| Γλυφάδας | 2 | [PDF](http://www.glyfada.gr/appdata/documents/dhmotikisygkoinvnia.pdf), Moovit | C | none |
| Αθηναίων | 0 today, 4 planned (2027) | press only | – | – |
| 22 others: Αιγάλεω, Αλίμου, Ασπροπύργου, Αχαρνών, Βάρης-Βούλας-Βουλιαγμένης, Βριλησσίων, Διονύσου, Ζωγράφου, Ηλιούπολης, Καισαριανής, Κερατσινίου-Δραπετσώνας, Κορυδαλλού, Κορωπίου, Λαυρεωτικής, Μεγάρων, Μοσχάτου-Ταύρου, Νέας Φιλαδέλφειας-Χαλκηδόνας, Νίκαιας-Ρέντη, Παλλήνης, Πεντέλης, Πετρούπολης, Ραφήνας-Πικερμίου, Χαϊδαρίου | 1-4 each | municipal page / PDF / image only (links in [athenstransport.com/dimotiki](https://www.athenstransport.com/dimotiki/), 2023) | C | none |

OSM query (2026-11-03, Overpass): `relation[type=route][route~"^(bus|minibus)$"](37.60,23.25,38.35,24.10)`
filtered on `operator~"Δήμος"` or `network~"Δημοτικ"`: 37 relations, 12 municipalities; 27
with stop members, 10 without. No relation has a timetable (`opening_hours`/`interval`).

## Licence notes

- OSM data is ODbL 1.0: free to use with attribution («© OpenStreetMap contributors»);
  a derived *database* that we publish must stay ODbL. Showing it on a map is a «produced
  work»: attribution is enough.
- Municipal web pages and PDFs carry no open licence. Timetable facts are not creative works,
  but copying a whole PDF or map image is not safe; retyping times needs no licence in
  practice, but ask the municipality if we publish them as a dataset.
- Live apps (3pi Telematics for Μεταμόρφωση, from the web domain; vendors of Νέα Ιωνία and Νέα
  Σμύρνη not named) have no public API. Reusing their endpoints without permission is out.

## Recommendation

A **schedule-only** layer is not worth it now. Times exist only in PDFs and images for ~37
municipalities, many from 2017-2023; we would retype and re-check them by hand, and a wrong
time is worse than none. These lines run 4-17 trips per weekday, so a stale timetable fails
often.

A **cheap «Δημοτικά» layer from OSM is worth it**: line geometry and stops for the 12
municipalities in OSM (about a third of the lines, the dense north and south suburbs), refreshed from
Overpass weekly, ODbL attribution, and a link from each line to the municipality's timetable
page. Effort: about 1 day backend (Overpass import, one endpoint) + 1 day web. No timetable,
no ETA, labelled «πρόγραμμα: δείτε τον δήμο».

Later, only if users use the layer: ask the three municipalities with live apps (Μεταμόρφωση,
Νέα Ιωνία, Νέα Σμύρνη) or their vendor for a GTFS-RT or JSON feed; add stops in OSM for the
10 relations without them.

## Sources

- [athenstransport.com: Οι δημοτικές συγκοινωνίες της Αττικής](https://www.athenstransport.com/dimotiki/) (37 municipalities, updated 2023-03-23)
- [Μεταμόρφωση: Δημοτική Συγκοινωνία / app](https://metamorfossi.gov.gr/katoikoi/metakinisi/dimotiki-sugkoinonia), [news](https://metamorfossi.gov.gr/-1577)
- [Νέα Ιωνία: τηλεματική, app «nea ionia bus»](https://neaionia.gr/dimosieuseis/perase-sti-nea-epochi-i-dimotiki-sygkoinonia-tis-neas-ionias)
- [Νέα Σμύρνη: τηλεματική στη δημοτική συγκοινωνία](https://neasmyrni.gr/%CE%B7-%CF%84%CE%B7%CE%BB%CE%B5%CE%BC%CE%B1%CF%84%CE%B9%CE%BA%CE%B7-%CF%83%CF%84%CE%B7%CE%BD-%CF%85%CF%80%CE%B7%CF%81%CE%B5%CF%83%CE%B9%CE%B1-%CF%84%CE%B7%CF%83-%CE%B4%CE%B7%CE%BC%CE%BF%CF%84%CE%B9/)
- [Δήμος Αθηναίων: νέες γραμμές «Athens Move»](https://zhteitai.gr/newsroom/2935-dimos-athinaion-tesseris-nees-grammes-sigkoinonias-me-17-leoforeia-26-3os.html)
- Moovit line pages: [Πειραιάς](https://moovitapp.com/index/en/public_transit-lines-Athens_%CE%91%CE%B8%CE%B7%CE%BD%CE%B1-1822-1984516), [Γλυφάδα](https://moovitapp.com/index/en/public_transit-lines-Athens_%CE%91%CE%B8%CE%B7%CE%BD%CE%B1-1822-864855), [Νέα Σμύρνη](https://moovitapp.com/index/en/public_transit-lines-Athens_%CE%91%CE%B8%CE%B7%CE%BD%CE%B1-1822-857927), [Άγιος Δημήτριος](https://moovitapp.com/index/en/public_transit-lines-Athens_%CE%91%CE%B8%CE%B7%CE%BD%CE%B1-1822-1074545)
- [Mobility Database: Greek feeds](https://mobilitydatabase.org/feeds/gtfs/mdb-3220) (OASA only, no municipal feed)
- OpenStreetMap via Overpass API, 2026-11-03; [ODbL](https://opendatacommons.org/licenses/odbl/)
