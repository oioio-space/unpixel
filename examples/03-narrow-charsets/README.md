# 03 · Narrow charsets — PINs, tokens and structured secrets

**What this shows:** the *charset* (the alphabet UnPixel searches) is your biggest
speed and accuracy lever. If you know the secret is numeric, search only digits; if
it's a hex token, only `0-9a-f`. A tighter alphabet shrinks the search space
(`charset^length`) and removes look-alike wrong answers before they can win.

## The images in this folder

| image | kind | hidden text | good charset |
|-------|------|-------------|--------------|
| `images/pin_digits.png`   | numeric PIN | `1234` | digits |
| `images/token_alnum.png`  | alphanumeric token | `Go2` | letters + digits |
| `images/expr_symbols.png` | symbols | `x=1` | `x=1 +-_a0` |

## For everyone — the CLI

Use a named preset with `--charset-preset`, or spell the exact set with `--charset`:

```console
$ unpixel --redaction mosaic --charset-preset digits -b 8 images/pin_digits.png
1234

$ unpixel --redaction mosaic --charset "x=1 +-_a0" -b 8 images/expr_symbols.png
x=1
```

Presets: `digits`, `hex`, `lower`, `alnum`, `ascii`/`code`.

## For developers — the code

Pass the alphabet with [`unpixel.WithCharset`](https://pkg.go.dev/github.com/oioio-space/unpixel#WithCharset);
the constants [`CharsetDigits`](https://pkg.go.dev/github.com/oioio-space/unpixel#pkg-constants),
`CharsetHex`, `CharsetAlnum`, … are ready-made. See [`main.go`](main.go):

```go
res, _ := unpixel.Recover(ctx, img,
    unpixel.WithCharset(unpixel.CharsetDigits), // "0123456789"
    unpixel.WithBlockSize(8),
)
fmt.Println(res.BestGuess) // "1234"
```

```console
$ go run .
pin_digits.png     -> "1234"  (a 4-digit PIN (digits only))
token_alnum.png    -> "Go2"   (a short alphanumeric token)
expr_symbols.png   -> "x=1"   (an expression with symbols)
```

## Advanced

- Include a **space** in the charset if the hidden text can contain one.
- The **order** of characters doesn't affect correctness, only tie-break determinism.
- For high-entropy secrets where you can't narrow the alphabet, prefer the
  **propose-and-verify** approach ([`../04-propose-and-verify`](../04-propose-and-verify)):
  supply candidate strings and let UnPixel confirm the physical match instead of
  searching an enormous space.
