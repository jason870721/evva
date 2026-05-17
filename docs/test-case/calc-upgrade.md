Test the calc tool end-to-end. Run each expression below and confirm the output matches. Report any mismatch.

## New feature: modulo (%)

1. `10 % 3`        → 1
2. `10.5 % 3`      → 1.5
3. `1 % 0`         → error with "modulo by zero"
4. `10 + 3 % 2`    → 11  (modulo before add, same as * and /)
5. `10 % 3 * 2`    → 2   (left-to-right, same precedence as * and /)

## New feature: ** error message

6. `2 ** 10`       → error with "** is not supported; use ^"

## Regression checks (should still work)

7.  `2 + 3 * 4`           → 14
8.  `(2 + 3) * 4`         → 20
9.  `10 / 3`              → 3.333333
10. `2 ^ 10`              → 1024
11. `((1+2)*3/4)^5`       → 57.665039
12. `-5 + 3 * -2`         → -11
13. `0.1 + 0.2`           → 0.3
14. `1 / 0`               → error with "division by zero"
15. `+5 * +3`             → 15
16. `2 ^ -1`              → 0.5
17. `2 ^ 3 ^ 2`           → 512
18. `  1  +  2  `         → 3
19. `1e3 + 1`             → 1001
