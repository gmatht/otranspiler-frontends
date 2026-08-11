// Rules.java — which grammar rules (CST node types) do the examples
// actually exercise? Parses every file with the generated parser,
// and for PARSE-CLEAN files (zero lexer+parser errors) records every
// parser rule entered (via the parse-tree walk) and every token type
// consumed (via the lexer vocabulary).
//
// Usage: java -cp <out>:<antlr4.jar> Rules <posix|py> <file>...
import org.antlr.v4.runtime.*;
import org.antlr.v4.runtime.tree.*;
import java.nio.file.*;
import java.util.*;

public class Rules {
    public static void main(String[] args) throws Exception {
        String mode = args[0];
        Map<String, Integer> ruleCounts = new TreeMap<>();
        Map<String, Integer> tokenCounts = new TreeMap<>();
        String[] ruleNames = null;
        int clean = 0, total = 0;
        for (int i = 1; i < args.length; i++) {
            String p = args[i];
            String src;
            try {
                src = Files.readString(Paths.get(p));
            } catch (Exception e) {
                continue;
            }
            total++;
            final int[] nerr = {0};
            BaseErrorListener el = new BaseErrorListener() {
                public void syntaxError(Recognizer<?, ?> r, Object o, int line,
                        int col, String msg, RecognitionException e) {
                    nerr[0]++;
                }
            };
            try {
                if (mode.equals("posix")) {
                    POSIXLexer lx = new POSIXLexer(CharStreams.fromString(src));
                    lx.removeErrorListeners(); lx.addErrorListener(el);
                    CommonTokenStream toks = new CommonTokenStream(lx);
                    POSIXParser ps = new POSIXParser(toks);
                    ps.removeErrorListeners(); ps.addErrorListener(el);
                    ParseTree tree = ps.start();
                    ruleNames = ps.getRuleNames();
                    if (nerr[0] == 0) {
                        clean++;
                        collect(tree, ruleNames, ruleCounts);
                        toks.fill();
                        for (Token t : toks.getTokens()) {
                            String name = lx.getVocabulary().getSymbolicName(t.getType());
                            if (name != null) tokenCounts.merge(name, 1, Integer::sum);
                        }
                    }
                } else {
                    Python3Lexer lx = new Python3Lexer(CharStreams.fromString(src));
                    lx.removeErrorListeners(); lx.addErrorListener(el);
                    CommonTokenStream toks = new CommonTokenStream(lx);
                    Python3Parser ps = new Python3Parser(toks);
                    ps.removeErrorListeners(); ps.addErrorListener(el);
                    ParseTree tree = ps.file_input();
                    ruleNames = ps.getRuleNames();
                    if (nerr[0] == 0) {
                        clean++;
                        collect(tree, ruleNames, ruleCounts);
                        toks.fill();
                        for (Token t : toks.getTokens()) {
                            String name = lx.getVocabulary().getSymbolicName(t.getType());
                            if (name != null) tokenCounts.merge(name, 1, Integer::sum);
                        }
                    }
                }
            } catch (Throwable e) {
                // unparseable — contributes nothing to the used set
            }
        }
        System.out.println("RULES-DEFINED " + ruleNames.length);
        System.out.println("RULES-EXERCISED " + ruleCounts.size() + " (by " + clean + "/" + total
            + " parse-clean files)");
        for (Map.Entry<String, Integer> e : ruleCounts.entrySet()) {
            System.out.println("RULE\t" + e.getKey() + "\t" + e.getValue());
        }
        System.out.println("TOKENS-EXERCISED " + tokenCounts.size());
        for (Map.Entry<String, Integer> e : tokenCounts.entrySet()) {
            System.out.println("TOKEN\t" + e.getKey() + "\t" + e.getValue());
        }
    }

    static void collect(ParseTree t, String[] ruleNames, Map<String, Integer> used) {
        if (t instanceof RuleContext) {
            RuleContext rc = (RuleContext) t;
            int idx = rc.getRuleIndex();
            if (idx >= 0 && idx < ruleNames.length) {
                used.merge(ruleNames[idx], 1, Integer::sum);
            }
            for (int i = 0; i < rc.getChildCount(); i++) {
                collect(rc.getChild(i), ruleNames, used);
            }
        }
    }
}
