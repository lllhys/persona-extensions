# Third-party notices

## ECDICT

The word glosses in `dict/lexicon.json.gz` are a subset of [ECDICT](https://github.com/skywind3000/ECDICT) by Linwei, used for lookup and for choosing which words are vocabulary candidates. ECDICT is distributed under the MIT license:

```text
MIT License

Copyright (c) 2025 Linwei

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

`dict/build.py` rebuilds the subset from `ecdict.csv`. It keeps headwords whose corpus frequency rank is outside the most common 5000 and at most 20000, plus Collins or Oxford headwords outside that basic band, and inflected forms that point at those headwords. The shipped bytes are the subset, not the upstream CSV.

## Termwise

[s546126/termwise](https://github.com/s546126/termwise) is the companion terminal and coding-agent vocabulary tool (`tw`). This extension implements the Persona reading page and browser preview in Go, using the same local ECDICT candidate rules (`exchange` inflections `0:` and `p/d/i/3/s/r/t`, short uppercase acronym skipping, function-word filtering, and the everyday developer word list in `devwords.txt` from `termwise/devwords.txt`).
