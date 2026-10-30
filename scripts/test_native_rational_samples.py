#!/usr/bin/env python3
"""将实际C++固定宽有理数实现与Python Fraction独立对照，不需要媒体SDK。"""
from fractions import Fraction
import os
from pathlib import Path
import random
import shutil
import subprocess
import tempfile
import unittest


class NativeRationalSamples(unittest.TestCase):
    def test_exact_endpoints_against_independent_fraction_oracle(self):
        compiler = shutil.which(os.environ.get("CXX", "c++"))
        if compiler is None:
            self.skipTest("缺少独立C++编译器，未运行有理数原生对照")
        root = Path(__file__).resolve().parent.parent
        source = r'''
#include "rational_samples.h"
#include <iostream>
int main(){
  char op;
  while(std::cin>>op){
    int64_t n,d,o,sn,sd;int a,b;
    if(op=='m'){
      if(!(std::cin>>n>>d>>o))return 2;
      try{std::cout<<aividcut_samples::microsecond_sample(n,d,o)<<'\n';}
      catch(const std::exception&){std::cout<<"ERR\n";}
    }else if(op=='t'){
      if(!(std::cin>>n>>d))return 2;
      try{std::cout<<aividcut_samples::signed_microsecond_sample(n,d)<<'\n';}
      catch(const std::exception&){std::cout<<"ERR\n";}
    }else{
      if(!(std::cin>>n>>d>>sn>>sd>>o>>a>>b))return 2;
      try{std::cout<<aividcut_samples::shifted_start(n,d,sn,sd,o,a,b)<<'\n';}
      catch(const std::exception&){std::cout<<"ERR\n";}
    }
  }
}
'''
        maximum = (1 << 63) - 1
        rng = random.Random(20261025)
        special = [1, 3, 6, 125, 48000, 10**13, 10**14, maximum]
        cases = []
        expected = []

        def add_micro(n, d, origin):
            cases.append(f"m {n} {d} {origin}")
            value = (Fraction(n, d) - origin) * Fraction(6, 125)
            expected.append(str(-((-value.numerator) // value.denominator)))

        for denominator in special:
            for numerator in [0, 1, 125, maximum]:
                for origin in [0, 1, 500000, maximum]:
                    add_micro(numerator, denominator, origin)
        for _ in range(4000):
            add_micro(rng.randrange(maximum), rng.choice(special) if rng.randrange(2) else rng.randrange(1, maximum), rng.randrange(maximum))
        for _ in range(1000):
            n = rng.randrange(-maximum-1,maximum)
            d = rng.choice(special)
            source_d = rng.choice(special)
            origin = rng.randrange(1, maximum)
            source_n = rng.randrange(min(maximum, origin * source_d))
            speed_n = rng.choice([1, 2, 3, 1001, 999999, 2147483647])
            speed_d = rng.choice([1, 2, 3, 1001, 999999, 2147483647])
            cases.append(f"s {n} {d} {source_n} {source_d} {origin} {speed_n} {speed_d}")
            value = (Fraction(n, d) + (origin - Fraction(source_n, source_d)) * Fraction(speed_d, speed_n)) * Fraction(6, 125)
            result = -((-value.numerator) // value.denominator)
            expected.append("ERR" if result > maximum or result < -maximum-1 else str(result))
        for n in [-maximum-1, -maximum, -125, -1, 0, 1, maximum]:
            for d in special:
                cases.append(f"t {n} {d}")
                value = Fraction(n,d)*Fraction(6,125)
                expected.append(str(-((-value.numerator)//value.denominator)))
        for _ in range(1000):
            n,d=rng.randrange(-maximum-1,maximum),rng.choice(special)
            cases.append(f"t {n} {d}")
            value=Fraction(n,d)*Fraction(6,125)
            expected.append(str(-((-value.numerator)//value.denominator)))
        with tempfile.TemporaryDirectory(prefix="aividcut rational ") as temporary:
            folder = Path(temporary)
            cpp = folder / "probe.cpp"
            cpp.write_text(source)
            binary = folder / "probe"
            subprocess.run([compiler, "-std=c++17", "-O1", "-g", "-fsanitize=undefined,address", "-fno-omit-frame-pointer", "-I", str(root / "native/mlt-worker"), str(cpp), "-o", str(binary)], check=True, capture_output=True, text=True)
            environment = os.environ.copy()
            environment["ASAN_OPTIONS"] = "detect_leaks=0:halt_on_error=1"
            environment["UBSAN_OPTIONS"] = "halt_on_error=1"
            run = subprocess.run([str(binary)], input="\n".join(cases)+"\n", text=True, capture_output=True, check=True, env=environment, timeout=30)
            actual = run.stdout.splitlines()
            self.assertEqual(len(actual), len(expected))
            for index, (observed, wanted) in enumerate(zip(actual, expected)):
                self.assertEqual(observed, wanted, f"第{index}个输入：{cases[index]}")
            self.assertNotIn("runtime error:", run.stderr)
            print(f"实际C++ + ASan/UBSan：{len(cases)}个端点/延迟与独立Fraction一致")


if __name__ == "__main__":
    unittest.main(verbosity=2)
