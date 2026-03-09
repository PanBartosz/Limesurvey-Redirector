# User Tutorial / Instrukcja Użytkownika

This is a short guide for route users.

The old app workflow was:
- choose the LimeSurvey version
- enter the short link
- paste survey IDs line by line

The new app keeps the same core idea, but maps it to:
- choose the configured LimeSurvey instance
- enter the route slug
- add survey IDs as route targets

## Polski

### Do czego służy aplikacja

Aplikacja tworzy jeden publiczny link do badania i kieruje respondentów do jednej z kilku ankiet LimeSurvey, zgodnie z wybranym algorytmem.

To jest przydatne, gdy:
- masz kilka kopii tej samej ankiety
- chcesz równomiernie rozkładać ruch
- chcesz udostępniać tylko jeden link w mailu, QR kodzie albo formularzu rekrutacyjnym

### Kto co robi

- `Admin` konfiguruje instancje LimeSurvey
- `Użytkownik` tworzy i zarządza własnymi trasami (`routes`)

Jeżeli nie widzisz właściwej instancji na liście, poproś admina o jej dodanie.

### Szybki workflow

1. Zaloguj się do panelu.
2. Otwórz `Routes`.
3. W sekcji `Create route` wpisz:
   - `Name`: nazwa robocza, np. `Badanie pamięci 2026`
   - `Slug`: końcówka linku publicznego, np. `pamiec-2026`
4. Wybierz `Instance`.
   - To odpowiednik wyboru wersji LimeSurvey w starej aplikacji.
5. Dodaj ankiety w sekcji `Surveys in this route`.
   - Jeden wiersz = jedna ankieta LimeSurvey.
   - Wpisz `Survey ID`.
   - Jeżeli używasz algorytmu ważonego, ustaw też `Weight`.
6. Wybierz `Algorithm`.
7. W razie potrzeby ustaw opcje dodatkowe:
   - `Fallback URL`
   - `Forward Query Mode`
   - `Pending assignment buffer`
   - `Stickiness`
8. Kliknij `Create Route`.
9. Po zapisaniu skopiuj `Public URL` z karty trasy.
10. Udostępniaj tylko ten publiczny link uczestnikom.

### Jak wybrać algorytm

- `Completed Fuzzy`: dobry domyślny wybór dla kilku kopii tej samej ankiety
- `Least Completed`: najprostsze wyrównywanie po liczbie ukończonych odpowiedzi
- `Weighted Completed` / `Weighted Fuzzy`: używaj tylko wtedy, gdy chcesz świadomie wysyłać więcej ruchu do niektórych ankiet niż do innych
- `Random`: tylko gdy naprawdę chcesz losować bez wyrównywania

Jeżeli nie masz specjalnego powodu, zacznij od `Completed Fuzzy`.

### Co oznaczają najważniejsze opcje

- `Fallback URL`
  - link awaryjny używany wtedy, gdy żadna ankieta nie może przyjąć ruchu
- `Forward Query Mode`
  - decyduje, czy parametry z linku wejściowego, np. `token`, mają zostać przekazane dalej do ankiety
- `Pending assignment buffer`
  - pomaga unikać chwilowego przeciążenia jednej ankiety, zanim statystyki LimeSurvey zdążą się odświeżyć
- `Stickiness`
  - próbuje kierować tego samego respondenta z powrotem do tej samej ankiety

### Najczęstszy scenariusz

Użyj tego, jeśli chcesz odtworzyć zachowanie starej aplikacji:

1. Admin dodaje właściwą instancję LimeSurvey.
2. Ty tworzysz nową trasę.
3. Wybierasz instancję.
4. Dodajesz identyfikatory ankiet, po jednej ankiecie na wiersz.
5. Wybierasz `Completed Fuzzy` albo `Least Completed`.
6. Zapisujesz trasę.
7. Kopiujesz `Public URL`.

To jest nowy odpowiednik:
- stare `wersja LimeSurvey` -> nowe `Instance`
- stare `Link skrócony` -> nowe `Slug`
- stare `Numery ankiet linijka po linijce` -> nowe `Surveys in this route`

### Dobre praktyki

- Wszystkie ankiety w trasie powinny być aktywne.
- Używaj tylko ankiet, które naprawdę są kopiami tego samego badania.
- `Slug` musi być unikalny w całym systemie.
- Jeżeli używasz algorytmu ważonego, sprawdź dwa razy wagi.
- Po utworzeniu trasy zawsze testowo otwórz `Public URL`.

## English

### What this app does

The app creates one public study link and redirects respondents to one of several LimeSurvey surveys using the selected routing algorithm.

This is useful when:
- you have multiple clones of the same survey
- you want to balance traffic across them
- you want to share only one link in email, QR codes, or recruitment flows

### Who does what

- `Admin` configures LimeSurvey instances
- `User` creates and manages their own routes

If you do not see the correct instance in the list, ask the admin to add it.

### Quick workflow

1. Log in to the panel.
2. Open `Routes`.
3. In `Create route`, enter:
   - `Name`: internal label, for example `Memory Study 2026`
   - `Slug`: the public link suffix, for example `memory-2026`
4. Select `Instance`.
   - This is the new equivalent of choosing the LimeSurvey version in the old app.
5. Add surveys in `Surveys in this route`.
   - One row = one LimeSurvey survey.
   - Enter the `Survey ID`.
   - If you use a weighted algorithm, also set `Weight`.
6. Select the `Algorithm`.
7. Configure optional settings if needed:
   - `Fallback URL`
   - `Forward Query Mode`
   - `Pending assignment buffer`
   - `Stickiness`
8. Click `Create Route`.
9. After saving, copy the `Public URL` from the route card.
10. Share only that public URL with respondents.

### How to choose an algorithm

- `Completed Fuzzy`: good default for several clones of the same survey
- `Least Completed`: simplest balancing by completed responses
- `Weighted Completed` / `Weighted Fuzzy`: use only when some surveys should intentionally receive more traffic than others
- `Random`: only if you really want random selection without balancing

If you do not have a specific reason, start with `Completed Fuzzy`.

### What the main options mean

- `Fallback URL`
  - emergency destination used when no survey target can accept traffic
- `Forward Query Mode`
  - controls whether incoming query parameters such as `token` are forwarded to the final survey URL
- `Pending assignment buffer`
  - helps avoid temporary overloading of one survey before LimeSurvey stats catch up
- `Stickiness`
  - tries to send the same respondent back to the same survey target

### Most common scenario

Use this if you want the same basic workflow as the old app:

1. Admin adds the correct LimeSurvey instance.
2. You create a new route.
3. You select the instance.
4. You add survey IDs, one survey per row.
5. You choose `Completed Fuzzy` or `Least Completed`.
6. You save the route.
7. You copy the `Public URL`.

This is the new equivalent of:
- old `LimeSurvey version` -> new `Instance`
- old `short link` -> new `Slug`
- old `survey IDs pasted line by line` -> new `Surveys in this route`

### Good practice

- All surveys in the route should be active.
- Only group surveys that are actual clones of the same study.
- `Slug` must be unique system-wide.
- If you use weighted algorithms, double-check the weights.
- After creating a route, always open the `Public URL` once as a smoke test.
